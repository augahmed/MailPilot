package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func chunkFixture(t *testing.T, s *Store, a domain.AccountID, provider string) (domain.Email, repository.ChunkSnapshot) {
	t.Helper()
	e := sampleEmail(a, provider)
	e.BodyState = "complete"
	e.BodyText = "First section. Second section."
	e.ID = saveEmail(t, s, e)
	stored, err := s.Emails().Get(context.Background(), a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, repository.ChunkSnapshot{AccountID: a, EmailID: e.ID, SourceRevision: stored.ContentHash, PreprocessingVersion: "clean-v1", IndexVersion: "chunks-v1", Model: repository.EmbeddingModel{ID: "fixture", Version: "recipe-v1", Dimension: 2}, Chunks: []repository.ChunkInput{
		{Index: 0, SectionType: "body", Text: "First section.", TokenCount: 3, StartOffset: 0, EndOffset: 14},
		{Index: 1, SectionType: "body", Text: "Second section.", TokenCount: 3, StartOffset: 15, EndOffset: 30},
	}}
}
func replaceChunks(t *testing.T, s *Store, input repository.ChunkSnapshot) (repository.IndexState, []repository.EmailChunk) {
	t.Helper()
	state, err := s.Chunks().Replace(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := s.Chunks().List(context.Background(), input.AccountID, input.EmailID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return state, chunks
}
func readyChunks(t *testing.T, s *Store, state repository.IndexState, chunks []repository.EmailChunk) repository.IndexState {
	t.Helper()
	batch := repository.EmbeddingBatch{State: state}
	for _, c := range chunks {
		batch.Embeddings = append(batch.Embeddings, repository.ChunkEmbedding{ChunkID: c.ID, Status: repository.EmbeddingReady, Vector: []float32{.25, .75}})
	}
	next, err := s.Chunks().SaveEmbeddings(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v; want %v", err, want)
	}
}

func TestChunksIdempotencyOrderingAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := account(t, s, "chunks")
	e, input := chunkFixture(t, s, a, "same")
	input.Chunks[0], input.Chunks[1] = input.Chunks[1], input.Chunks[0]
	state, chunks := replaceChunks(t, s, input)
	if state.Status != repository.IndexPending || state.LastIndexedAt != nil || chunks[0].Index != 0 || chunks[1].Index != 1 {
		t.Fatal("initial ordering/status")
	}
	if input.Chunks[0].Index != 1 || input.Chunks[0].ContentHash != "" {
		t.Fatal("mutated caller inputs")
	}
	state = readyChunks(t, s, state, chunks)
	if state.Status != repository.IndexReady || state.LastIndexedAt == nil {
		t.Fatal("ready state missing")
	}
	again, got := replaceChunks(t, s, input)
	if !reflect.DeepEqual(state, again) || !reflect.DeepEqual(chunks, got) {
		t.Fatal("replace is not idempotent")
	}
	again = readyChunks(t, s, again, got)
	if !reflect.DeepEqual(state, again) {
		t.Fatal("embedding writes are not idempotent")
	}
	// Provider label changes must not change the content identity or active generation.
	e.Labels = nil
	saveEmail(t, s, e)
	unchanged, err := s.Chunks().State(ctx, a, e.ID)
	if err != nil || !reflect.DeepEqual(state, unchanged) {
		t.Fatal("label-only change invalidated chunks", err)
	}
	page, err := s.Chunks().List(ctx, a, e.ID, repository.Page{Limit: 1, Offset: 1})
	if err != nil || len(page) != 1 || page[0].ID != chunks[1].ID {
		t.Fatal("unstable pagination", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	persisted, err := s.Chunks().State(ctx, a, e.ID)
	if err != nil || !reflect.DeepEqual(state, persisted) {
		t.Fatal("state did not survive restart", err)
	}
	vector, err := s.Chunks().Embedding(ctx, a, e.ID, chunks[0].ID, input.Model)
	if err != nil || !reflect.DeepEqual(vector.Vector, []float32{.25, .75}) {
		t.Fatal("vector did not survive restart", err)
	}
}

func TestChunksContentChangesReuseOnlyUnchangedInputs(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "reuse-a")
	b := account(t, s, "reuse-b")
	e, input := chunkFixture(t, s, a, "one")
	_, other := chunkFixture(t, s, b, "one")
	state, chunks := replaceChunks(t, s, input)
	state = readyChunks(t, s, state, chunks)
	otherState, otherChunks := replaceChunks(t, s, other)
	otherState = readyChunks(t, s, otherState, otherChunks)
	e.BodyText = "First section. Changed section."
	saveEmail(t, s, e)
	stale, err := s.Chunks().State(ctx, a, e.ID)
	if err != nil || stale.Status != repository.IndexStale || stale.Generation == state.Generation || stale.LastIndexedAt != nil {
		t.Fatal("source did not invalidate state", err)
	}
	_, err = s.Chunks().List(ctx, a, e.ID, repository.Page{Limit: 10})
	requireError(t, err, repository.ErrConflict)
	_, err = s.Chunks().Embedding(ctx, a, e.ID, chunks[0].ID, input.Model)
	requireError(t, err, repository.ErrConflict)
	_, err = s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}}})
	requireError(t, err, repository.ErrConflict)
	_, err = s.Chunks().Replace(ctx, input)
	requireError(t, err, repository.ErrConflict)
	stored, err := s.Emails().Get(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.SourceRevision = stored.ContentHash
	input.Chunks[1].Text = "Changed section."
	input.Chunks[1].EndOffset = 31
	next, nextChunks := replaceChunks(t, s, input)
	if next.Status != repository.IndexPartial || nextChunks[0].ID != chunks[0].ID || nextChunks[1].ID == chunks[1].ID {
		t.Fatal("incorrect reuse of changed/unchanged chunks")
	}
	for i, c := range nextChunks {
		vector, err := s.Chunks().Embedding(ctx, a, e.ID, c.ID, input.Model)
		if err != nil {
			t.Fatal(err)
		}
		want := repository.EmbeddingPending
		if i == 0 {
			want = repository.EmbeddingReady
		}
		if vector.Status != want {
			t.Fatal("incorrect cached vector status")
		}
	}
	got, err := s.Chunks().State(ctx, b, other.EmailID)
	if err != nil || !reflect.DeepEqual(got, otherState) {
		t.Fatal("other account invalidated", err)
	}
	// Metadata-only source revisions reuse vectors, but always rotate the work token.
	next = readyChunks(t, s, next, nextChunks)
	e.Subject = "A changed subject"
	saveEmail(t, s, e)
	stored, err = s.Emails().Get(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.SourceRevision = stored.ContentHash
	metadata, metadataChunks := replaceChunks(t, s, input)
	if metadata.Status != "ready" || metadata.Generation == next.Generation || metadataChunks[0].ID != nextChunks[0].ID || metadataChunks[1].ID != nextChunks[1].ID {
		t.Fatal("unchanged input vectors were discarded")
	}
}

func TestChunksModelAndPreprocessingVersions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "models")
	_, input := chunkFixture(t, s, a, "one")
	first, chunks := replaceChunks(t, s, input)
	first = readyChunks(t, s, first, chunks)
	// Reusing a model version with an incompatible dimension must fail atomically.
	bad := input
	bad.Model.Dimension = 3
	_, err := s.Chunks().Replace(ctx, bad)
	requireError(t, err, repository.ErrConflict)
	input.Model.Version = "recipe-v2"
	second, secondChunks := replaceChunks(t, s, input)
	if second.Status != "pending" || second.Generation == first.Generation || secondChunks[0].ID != chunks[0].ID {
		t.Fatal("model change did not isolate cached vectors")
	}
	input.Model.Version = "recipe-v1"
	back, backChunks := replaceChunks(t, s, input)
	if back.Status != "ready" || back.Generation == first.Generation || backChunks[0].ID != chunks[0].ID {
		t.Fatal("cannot reuse model cache after switching back")
	}
	_, err = s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: first, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}}})
	requireError(t, err, repository.ErrConflict)
	input.PreprocessingVersion = "clean-v2"
	clean, cleanChunks := replaceChunks(t, s, input)
	if clean.Status != "pending" || cleanChunks[0].ID == chunks[0].ID {
		t.Fatal("preprocessing changes retained old vectors")
	}
	if count(t, s, "chunk_embeddings") != 2 {
		t.Fatal("obsolete chunk vectors did not cascade")
	}
}

func TestChunksPartialAndUnavailableBodies(t *testing.T) {
	ctx := context.Background()
	for _, body := range []string{"", "unavailable", "complete"} {
		t.Run("body="+body, func(t *testing.T) {
			s := testStore(t)
			a := account(t, s, "partial")
			e, input := chunkFixture(t, s, a, "one")
			e.BodyState = body
			e.BodyText = ""
			e.BodyHTML = nil
			saveEmail(t, s, e)
			stored, err := s.Emails().Get(ctx, a, e.ID)
			if err != nil {
				t.Fatal(err)
			}
			input.SourceRevision = stored.ContentHash
			// Header/subject evidence remains representable even without a usable body.
			input.Chunks = []repository.ChunkInput{{Index: 0, SectionType: "subject", Text: "Subject", TokenCount: 1, EndOffset: 7}}
			state, chunks := replaceChunks(t, s, input)
			state = readyChunks(t, s, state, chunks)
			want := "partial"
			if body == "complete" {
				want = "ready"
			}
			if state.Status != want || state.BodyState != body || state.LastIndexedAt == nil {
				t.Fatal("misleading coverage state")
			}
			input.Chunks = nil
			empty, emptyChunks := replaceChunks(t, s, input)
			if empty.Status != "unavailable" || empty.LastIndexedAt != nil || len(emptyChunks) != 0 || count(t, s, "chunk_embeddings") != 0 {
				t.Fatal("empty projection retained derived data")
			}
		})
	}
}

func TestChunksAccountIsolation(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "isolation-a")
	b := account(t, s, "isolation-b")
	_, ia := chunkFixture(t, s, a, "same")
	_, ib := chunkFixture(t, s, b, "same")
	sa, ca := replaceChunks(t, s, ia)
	sb, cb := replaceChunks(t, s, ib)
	wrong := ia
	wrong.AccountID = b
	_, err := s.Chunks().Replace(ctx, wrong)
	requireError(t, err, repository.ErrNotFound)
	_, err = s.Chunks().List(ctx, b, ia.EmailID, repository.Page{Limit: 10})
	requireError(t, err, repository.ErrNotFound)
	_, err = s.Chunks().Embedding(ctx, b, ib.EmailID, ca[0].ID, ib.Model)
	requireError(t, err, repository.ErrNotFound)
	_, err = s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: sb, Embeddings: []repository.ChunkEmbedding{{ChunkID: cb[0].ID, Status: "ready", Vector: []float32{1, 0}}, {ChunkID: ca[0].ID, Status: "ready", Vector: []float32{1, 0}}}})
	requireError(t, err, repository.ErrNotFound)
	v, err := s.Chunks().Embedding(ctx, b, ib.EmailID, cb[0].ID, ib.Model)
	if err != nil || v.Status != "pending" {
		t.Fatal("cross-account batch partially committed", err)
	}
	_, err = s.db.Exec(`INSERT INTO chunk_embeddings(account_id,chunk_id,model_id,model_version,dimension,status) VALUES(?,?,?,?,?,'pending')`, b, ca[0].ID, "foreign", "v1", 2)
	if err == nil {
		t.Fatal("cross-account foreign key accepted")
	}
	// Local ownership cannot be reassigned to another message, even within an account.
	_, err = s.db.Exec(`UPDATE email_chunks SET account_id=?,email_id=?,source_revision=? WHERE id=?`, b, ib.EmailID, ib.SourceRevision, ca[0].ID)
	if err == nil {
		t.Fatal("chunk identity reassigned")
	}
	unchanged, err := s.Chunks().State(ctx, a, ia.EmailID)
	if err != nil || !reflect.DeepEqual(unchanged, sa) {
		t.Fatal("foreign writes changed source account", err)
	}
}

func TestChunksPartialWritesRollback(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "atomic")
	_, input := chunkFixture(t, s, a, "one")
	state, chunks := replaceChunks(t, s, input)
	// Fail on the second embedding after the first write and its triggers ran.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_second_vector BEFORE UPDATE ON chunk_embeddings WHEN NEW.chunk_id='` + chunks[1].ID + `' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	batch := repository.EmbeddingBatch{State: state}
	for _, c := range chunks {
		batch.Embeddings = append(batch.Embeddings, repository.ChunkEmbedding{ChunkID: c.ID, Status: "ready", Vector: []float32{1, 0}})
	}
	_, err := s.Chunks().SaveEmbeddings(ctx, batch)
	requireError(t, err, repository.ErrConflict)
	v, err := s.Chunks().Embedding(ctx, a, input.EmailID, chunks[0].ID, input.Model)
	if err != nil || v.Status != "pending" {
		t.Fatal("first vector escaped rollback", err)
	}
	after, err := s.Chunks().State(ctx, a, input.EmailID)
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatal("state escaped rollback", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_second_vector; CREATE TRIGGER fail_second_chunk BEFORE INSERT ON email_chunks WHEN NEW.chunk_index=1 BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.PreprocessingVersion = "different"
	_, err = s.Chunks().Replace(ctx, changed)
	requireError(t, err, repository.ErrConflict)
	got, err := s.Chunks().List(ctx, a, input.EmailID, repository.Page{Limit: 10})
	if err != nil || !reflect.DeepEqual(chunks, got) {
		t.Fatal("replacement escaped rollback", err)
	}
	after, err = s.Chunks().State(ctx, a, input.EmailID)
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatal("replacement state escaped rollback", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Chunks().Replace(canceled, input)
	requireError(t, err, context.Canceled)
	_, err = s.Chunks().SaveEmbeddings(canceled, batch)
	requireError(t, err, context.Canceled)
}

func TestChunksValidateVectorsAndStoredCorruption(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "validation")
	_, input := chunkFixture(t, s, a, "one")
	state, chunks := replaceChunks(t, s, input)
	for _, vector := range [][]float32{nil, {1}, {1, 2, 3}, {0, 0}, {float32(math.NaN()), 1}, {float32(math.Inf(1)), 1}, {1, float32(math.Inf(-1))}} {
		_, err := s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: vector}}})
		requireError(t, err, repository.ErrInvalid)
	}
	_, err := s.db.Exec(`UPDATE chunk_embeddings SET status='ready',vector_data=? WHERE chunk_id=?`, []byte{0}, chunks[0].ID)
	if err == nil {
		t.Fatal("invalid BLOB length accepted")
	}
	// SQLite can enforce byte counts, but finite float values require Go validation.
	corrupt := make([]byte, 8)
	binary.LittleEndian.PutUint32(corrupt, math.Float32bits(float32(math.NaN())))
	if _, err = s.db.Exec(`UPDATE chunk_embeddings SET status='ready',vector_data=? WHERE chunk_id=?`, corrupt, chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Chunks().Embedding(ctx, a, input.EmailID, chunks[0].ID, input.Model)
	requireError(t, err, repository.ErrStorage)
	_, err = s.Chunks().Replace(ctx, input)
	requireError(t, err, repository.ErrStorage)
	if _, err = s.db.Exec(`UPDATE chunk_embeddings SET status='failed',vector_data=NULL WHERE chunk_id=?`, chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	// Invalid hashes cannot be used to claim that corrupted text is a reusable input.
	if _, err = s.db.Exec(`UPDATE email_chunks SET content_hash=? WHERE id=?`, hashText("different"), chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Chunks().Replace(ctx, input)
	requireError(t, err, repository.ErrStorage)
}

func TestChunksValidateSnapshots(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "invalid")
	_, input := chunkFixture(t, s, a, "one")
	mutations := []func(*repository.ChunkSnapshot){
		func(i *repository.ChunkSnapshot) { i.SourceRevision = "provider-id" },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].Index = 2 },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].Text = "" },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].Text = "\xff" },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].TokenCount = 0 },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].EndOffset++ },
		func(i *repository.ChunkSnapshot) { i.Chunks[0].ContentHash = hashText("wrong") },
		func(i *repository.ChunkSnapshot) { i.PreprocessingVersion = "" },
		func(i *repository.ChunkSnapshot) { i.Model.Dimension = 0 },
	}
	for _, mutate := range mutations {
		bad := input
		bad.Chunks = append([]repository.ChunkInput(nil), input.Chunks...)
		mutate(&bad)
		_, err := s.Chunks().Replace(ctx, bad)
		requireError(t, err, repository.ErrInvalid)
	}
	if count(t, s, "email_chunks") != 0 || count(t, s, "index_state") != 0 {
		t.Fatal("invalid snapshot persisted")
	}
	// Offsets measure bytes, including multibyte text, not Go rune positions.
	input.Chunks = []repository.ChunkInput{{Index: 0, SectionType: "body", Text: "café", TokenCount: 2, EndOffset: 5}}
	replaceChunks(t, s, input)
}

func TestChunksSyncDeletionAndPhysicalCascade(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "deleted")
	e, input := chunkFixture(t, s, a, "one")
	state, chunks := replaceChunks(t, s, input)
	readyChunks(t, s, state, chunks)
	plan := draftPlan(t, s, a)
	plan.EmailID = e.ID
	plan.Sources[0].ID = e.ID
	plan.Sources[0].Hash = input.SourceRevision
	plan.Digest = plan.Hash()
	importHistoricalDraft(t, s, plan, "uncertain")
	// Keep a historical journal bound to the same mail to exercise tombstone recovery.
	if _, err := s.db.Exec(`UPDATE reply_draft_actions SET email_id=? WHERE id=?`, e.ID, plan.ID); err == nil {
		t.Fatal("historical immutable plan unexpectedly permitted mutation")
	}
	next := repository.SyncCheckpoint{Phase: "history", Cursor: "fixture-cursor"}
	// A checkpoint failure must roll back source visibility and all derived deletes.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_chunk_checkpoint BEFORE INSERT ON sync_checkpoints BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.SyncCheckpoints().Commit(ctx, a, repository.SyncCheckpoint{}, next, nil, e.ProviderID)
	requireError(t, err, repository.ErrConflict)
	if count(t, s, "email_chunks") != len(chunks) || count(t, s, "chunk_embeddings") != len(chunks) {
		t.Fatal("derived deletion escaped checkpoint rollback")
	}
	if _, err = s.Chunks().Embedding(ctx, a, e.ID, chunks[0].ID, input.Model); err != nil {
		t.Fatal("rolled-back source became stale", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_chunk_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCheckpoints().Commit(ctx, a, repository.SyncCheckpoint{}, next, nil, e.ProviderID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"email_chunks", "chunk_embeddings", "index_state"} {
		if count(t, s, table) != 0 {
			t.Fatalf("%s survived deletion", table)
		}
	}
	_, err = s.Chunks().State(ctx, a, e.ID)
	requireError(t, err, repository.ErrNotFound)
	checkpoint, err := s.SyncCheckpoints().Load(ctx, a)
	if err != nil || checkpoint != next {
		t.Fatal("checkpoint lost", err)
	}
	journal, err := s.Replies().Get(ctx, a, plan.ID)
	if err != nil || journal.Plan.Digest != plan.Digest {
		t.Fatal("historical recovery altered", err)
	}
	var tombstone int
	if err = s.db.QueryRow(`SELECT remote_deleted FROM emails WHERE account_id=? AND id=?`, a, e.ID).Scan(&tombstone); err != nil || tombstone != 1 {
		t.Fatal("source tombstone lost", err)
	}
	_, second := chunkFixture(t, s, a, "two")
	replaceChunks(t, s, second)
	if _, err = s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, a, second.EmailID); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "email_chunks") != 0 || count(t, s, "chunk_embeddings") != 0 || count(t, s, "index_state") != 0 {
		t.Fatal("physical deletion did not cascade")
	}
}

func TestChunksReuseMovedAndDuplicateInputs(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "moved")
	_, input := chunkFixture(t, s, a, "one")
	state, chunks := replaceChunks(t, s, input)
	readyChunks(t, s, state, chunks)
	first, second := input.Chunks[0], input.Chunks[1]
	// Swapping ordinals must preserve each input's vector, without unique-key races.
	first.Index = 1
	second.Index = 0
	input.Chunks = []repository.ChunkInput{second, first}
	moved, got := replaceChunks(t, s, input)
	if moved.Status != "ready" || got[0].ID != chunks[1].ID || got[1].ID != chunks[0].ID {
		t.Fatal("moved vectors not reused")
	}
	// Repeated text needs separate ordered chunks, and only the already cached copy
	// may be ready. The new copy has not been embedded by this storage layer.
	first.Index = 2
	input.Chunks = append(input.Chunks, first)
	added, got := replaceChunks(t, s, input)
	if added.Status != "partial" || got[0].ID != chunks[1].ID || got[1].ID != chunks[0].ID || got[2].ID == chunks[0].ID {
		t.Fatal("duplicate chunk identity lost")
	}
	failed, err := s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: added, Embeddings: []repository.ChunkEmbedding{{ChunkID: got[2].ID, Status: repository.EmbeddingFailed}}})
	if err != nil || failed.Status != "partial" || failed.LastIndexedAt != nil {
		t.Fatal("failed vector claimed ready", err)
	}
	v, err := s.Chunks().Embedding(ctx, a, input.EmailID, got[2].ID, input.Model)
	if err != nil || v.Status != "failed" || len(v.Vector) != 0 {
		t.Fatal("failed status not persisted", err)
	}
}
