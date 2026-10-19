package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
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
	if s.SchemaVersion() != 15 {
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
