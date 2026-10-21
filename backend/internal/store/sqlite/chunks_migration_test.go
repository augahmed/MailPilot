package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"mailpilot.local/core/internal/repository"
	"path/filepath"
	"reflect"
	"testing"
)

// Build the actual historical schema instead of removing ledger entries from a
// current schema. This also keeps older migration tests valid as new DDL is added.
func historicalStore(t *testing.T, version int) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range files {
		if i >= version {
			break
		}
		body, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,0)`, i+1, fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db, version: version}, dir
}

func TestChunkMigrationUpgradesFourteenWithoutReinterpretingHistory(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 14)
	a := account(t, old, "upgrade-chunks")
	plan := draftPlan(t, old, a)
	importHistoricalDraft(t, old, plan, "uncertain")
	if _, err := old.db.Exec(`INSERT INTO email_embeddings VALUES(?,?,?,?,?,?,?)`, a, plan.EmailID, "historical", 2, "legacy", hashText("legacy"), make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.SchemaVersion() != 19 {
		t.Fatal("missing migration 015")
	}
	for _, table := range []string{"email_chunks", "chunk_embeddings", "index_state"} {
		if count(t, s, table) != 0 {
			t.Fatal("historical vectors were activated")
		}
	}
	if count(t, s, "email_embeddings") != 1 {
		t.Fatal("historical vectors lost")
	}
	action, err := s.Replies().Get(ctx, a, plan.ID)
	if err != nil || action.Plan.Digest != plan.Digest || action.State != "uncertain" {
		t.Fatal("historical journal changed", err)
	}
	row, err := s.Emails().Get(ctx, a, plan.EmailID)
	if err != nil || row.ContentHash != plan.Sources[0].Hash {
		t.Fatal("cached email changed", err)
	}
	var violations int
	if err = s.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatal("foreign keys invalid", err)
	}
}

func TestChunkMigrationFailureIsAtomic(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 14)
	a := account(t, old, "migration-rollback")
	plan := draftPlan(t, old, a)
	importHistoricalDraft(t, old, plan, "uncertain")
	// Fail after migration 015 has created its first table.
	if _, err := old.db.Exec(`CREATE TABLE chunk_embeddings(fixture TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err == nil {
		_ = s.Close()
		t.Fatal("conflicting schema accepted")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tables, ledger, journals int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('email_chunks','index_state')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("partial DDL committed", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&ledger); err != nil || ledger != 14 {
		t.Fatal("partial migration recorded", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM reply_draft_actions WHERE id=?`, plan.ID).Scan(&journals); err != nil || journals != 1 {
		t.Fatal("recovery evidence lost", err)
	}
}

func TestProvenanceMigrationPreservesExistingChunksAndVectors(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 15)
	a := account(t, old, "provenance-upgrade")
	_, input := chunkFixture(t, old, a, "one")
	c := input.Chunks[0]
	if _, err := old.db.Exec(`INSERT INTO email_chunks VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "old-chunk", a, input.EmailID, 0, c.SectionType, c.Text, c.TokenCount, input.SourceRevision, c.StartOffset, c.EndOffset, hashText(c.Text), input.PreprocessingVersion); err != nil {
		t.Fatal(err)
	}
	vector, err := encodeVector([]float32{.25, .75}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.db.Exec(`INSERT INTO chunk_embeddings VALUES(?,?,?,?,?,?,?)`, a, "old-chunk", input.Model.ID, input.Model.Version, 2, vector, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = old.db.Exec(`INSERT INTO index_state VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, a, input.EmailID, input.SourceRevision, input.PreprocessingVersion, input.IndexVersion, input.Model.ID, input.Model.Version, 2, "old-generation", "complete", "ready", 42); err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.Chunks().State(ctx, a, input.EmailID)
	if err != nil || state.ContentStatus != "complete" || state.Generation != "old-generation" || state.Status != "ready" {
		t.Fatal("existing index changed", err)
	}
	chunks, err := s.Chunks().List(ctx, a, input.EmailID, repository.Page{Limit: 10})
	if err != nil || len(chunks) != 1 || chunks[0].SourceKind != "" || chunks[0].ContextText != "" || chunks[0].ID != "old-chunk" {
		t.Fatal("legacy provenance invented", err)
	}
	cached, err := s.Chunks().Embedding(ctx, a, input.EmailID, "old-chunk", input.Model)
	if err != nil || !reflect.DeepEqual(cached.Vector, []float32{.25, .75}) {
		t.Fatal("existing vector altered", err)
	}
}

func TestProvenanceMigrationFailureRollsBackAddedColumns(t *testing.T) {
	old, dir := historicalStore(t, 15)
	if _, err := old.db.Exec(`CREATE TRIGGER chunks_mapping_changed AFTER UPDATE ON emails BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), dir)
	if err == nil {
		_ = s.Close()
		t.Fatal("conflicting migration accepted")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('email_chunks') WHERE name='source_kind'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial ALTER TABLE committed", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 15 {
		t.Fatal("partial ledger committed", err)
	}
}
