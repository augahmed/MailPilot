package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding/model"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

type searchEmbedder struct{ vector []float32 }

func (f searchEmbedder) Descriptor() model.Descriptor {
	return model.Descriptor{ModelID: "fixture", Version: "recipe-v1", Dimensions: len(f.vector), PreprocessingFingerprint: strings.Repeat("a", 64)}
}
func (f searchEmbedder) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	return f.vector, ctx.Err()
}
func searchFixture(t *testing.T, s *Store, a domain.AccountID, pid string) (repository.IndexState, []repository.EmailChunk, *retrieval.VectorSearch) {
	t.Helper()
	_, input := chunkFixture(t, s, a, pid)
	state, chunks := replaceChunks(t, s, input)
	state = readyChunks(t, s, state, chunks)
	search := retrieval.New(s.Vectors(), searchEmbedder{[]float32{.25, .75}})
	search.Profile.PreprocessingVersion = input.PreprocessingVersion
	search.Profile.IndexVersion = input.IndexVersion
	if _, err := s.db.Exec(`INSERT INTO local_index_models(account_id,model_id,model_version,dimension,fingerprint) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, a, search.Profile.Model.ID, search.Profile.Model.Version, search.Profile.Model.Dimension, search.Profile.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return state, chunks, search
}
func TestVectorSearchFiltersIsolationAndFreshness(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "search-a")
	b := account(t, s, "search-b")
	state, chunks, search := searchFixture(t, s, a, "one")
	_, _, _ = searchFixture(t, s, b, "one")
	_, other, _ := searchFixture(t, s, a, "two")
	got, err := search.Search(ctx, a, "find my section", 10, repository.SearchFilter{})
	if err != nil || len(got.Emails) != 2 || got.Scanned != 4 {
		t.Fatalf("search: %+v %v", got, err)
	}
	if len(got.Emails[0].Evidence) != 2 || got.Emails[0].Evidence[0].Score < .9999 {
		t.Fatal("lost evidence/cosine")
	}
	for _, source := range got.Sources {
		if source.Chunk.AccountID != a {
			t.Fatal("cross-account result")
		}
	}
	stored, err := s.Emails().Get(ctx, a, state.EmailID)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := s.Categories().Save(ctx, a, domain.EmailCategory{Name: "Search fixture", Description: "Synthetic search fixtures", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Categories().Assign(ctx, a, state.EmailID, cat); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filter repository.SearchFilter
		want   int
	}{
		{repository.SearchFilter{Sender: "ABSENT%_"}, 0}, {repository.SearchFilter{Sender: stored.Email.Sender.Address}, 2},
		{repository.SearchFilter{After: stored.Email.ReceivedAt.UnixMilli()}, 2}, {repository.SearchFilter{Before: stored.Email.ReceivedAt.UnixMilli()}, 0},
		{repository.SearchFilter{After: stored.Email.ReceivedAt.UnixMilli() + 1}, 0}, {repository.SearchFilter{CategoryID: string(cat)}, 1}, {repository.SearchFilter{CategoryID: "foreign"}, 0},
	} {
		result, e := search.Search(ctx, a, "find", 10, tc.filter)
		if e != nil || len(result.Emails) != tc.want {
			t.Fatalf("filter %+v: %d, %v", tc.filter, len(result.Emails), e)
		}
	}
	// Removing a category invalidates the previously filtered ephemeral result.
	filtered, err := search.Search(ctx, a, "find", 10, repository.SearchFilter{CategoryID: string(cat)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Categories().Unassign(ctx, a, state.EmailID, cat); err != nil {
		t.Fatal(err)
	}
	requireError(t, s.Vectors().CheckVectorSources(ctx, a, search.Profile, repository.SearchFilter{CategoryID: string(cat)}, filtered.Sources), repository.ErrConflict)
	// Incomplete generation: one pending vector excludes the entire message.
	_, err = s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "pending"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := search.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 1 || result.Emails[0].EmailID != string(other[0].EmailID) {
		t.Fatal("incomplete snapshot leaked", err)
	}
	requireError(t, s.Vectors().CheckVectorSources(ctx, a, search.Profile, repository.SearchFilter{}, got.Sources), repository.ErrConflict)
	readyChunks(t, s, state, chunks)
	stored.Email.BodyText += " changed"
	saveEmail(t, s, stored.Email)
	result, err = search.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 1 {
		t.Fatal("stale snapshot leaked", err)
	}
	incompatible := *search
	incompatible.Profile.PreprocessingVersion = "obsolete"
	result, err = incompatible.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 0 {
		t.Fatal("old preprocessing leaked")
	}
	incompatible = *search
	incompatible.Profile.Fingerprint = strings.Repeat("b", 64)
	result, err = incompatible.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 0 {
		t.Fatal("bad fingerprint leaked")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = search.Search(canceled, a, "find", 10, repository.SearchFilter{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestVectorSearchOverlapTieAndCorruption(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "dedup")
	_, input := chunkFixture(t, s, a, "overlap")
	input.Chunks = []repository.ChunkInput{
		{Index: 0, SectionType: "body", Text: "First section.", TokenCount: 3, SourceKind: "body_text", StartOffset: 0, EndOffset: 14, SectionEndOffset: 40},
		{Index: 1, SectionType: "body", Text: "section. Second", TokenCount: 3, SourceKind: "body_text", StartOffset: 6, EndOffset: 21, SectionEndOffset: 40},
		{Index: 2, SectionType: "body", Text: "Other evidence.", TokenCount: 3, SourceKind: "body_text", StartOffset: 25, EndOffset: 40, SectionEndOffset: 40},
	}
	state, chunks := replaceChunks(t, s, input)
	state = readyChunks(t, s, state, chunks)
	search := retrieval.New(s.Vectors(), searchEmbedder{[]float32{.25, .75}})
	search.Profile.PreprocessingVersion = input.PreprocessingVersion
	search.Profile.IndexVersion = input.IndexVersion
	if _, err := s.db.Exec(`INSERT INTO local_index_models(account_id,model_id,model_version,dimension,fingerprint) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, a, search.Profile.Model.ID, search.Profile.Model.Version, search.Profile.Model.Dimension, search.Profile.Fingerprint); err != nil {
		t.Fatal(err)
	}
	first, err := search.Search(ctx, a, "find", 1, repository.SearchFilter{})
	if err != nil || len(first.Emails) != 1 || len(first.Emails[0].Evidence) != 2 || first.Emails[0].Evidence[0].ChunkID != chunks[0].ID || first.Emails[0].Evidence[1].ChunkID != chunks[2].ID {
		t.Fatalf("dedup/tie: %+v %v", first, err)
	}
	raw, err := search.SearchChunks(ctx, a, "find", 2, repository.SearchFilter{})
	if err != nil || len(raw) != 2 || raw[0].Source.Chunk.ID != chunks[0].ID || raw[1].Source.Chunk.ID != chunks[1].ID {
		t.Fatal("exact top K chunks/ties", err)
	}
	// A higher-scoring overlapping chunk must replace the earlier evidence.
	_, err = s.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := search.Search(ctx, a, "find", 1, repository.SearchFilter{})
	if err != nil || result.Emails[0].Evidence[0].ChunkID != chunks[1].ID {
		t.Fatal("lost better overlapping evidence", err)
	}
	// Defensive malformed finite checks: invalid stored bytes fail the search closed.
	blob := make([]byte, 8)

	if _, err = s.db.Exec(`UPDATE chunk_embeddings SET vector_data=? WHERE chunk_id=?`, blob, chunks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE index_state SET status='ready',last_indexed_at=1 WHERE account_id=?`, a); err != nil {
		t.Fatal(err)
	}
	_, err = search.Search(ctx, a, "find", 1, repository.SearchFilter{})
	requireError(t, err, repository.ErrStorage)
}

func TestVectorSearchPartialDeletedAndCancelledScan(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "partial-deleted")
	_, _, search := searchFixture(t, s, a, "ready")
	e, input := chunkFixture(t, s, a, "partial")
	input.ContentStatus = "partial"
	state, chunks := replaceChunks(t, s, input)
	readyChunks(t, s, state, chunks)
	result, err := search.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 1 || result.Emails[0].EmailID == string(e.ID) {
		t.Fatal("partial generation returned", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	visits := 0
	err = s.Vectors().ScanVectors(cancelled, a, search.Profile, repository.SearchFilter{}, func(v repository.VectorSource) error { visits++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || visits != 1 {
		t.Fatal("scan cancellation failed", visits, err)
	}
	if _, err = s.db.Exec(`UPDATE emails SET remote_deleted=1 WHERE account_id=? AND id=?`, a, result.Emails[0].EmailID); err != nil {
		t.Fatal(err)
	}
	requireError(t, s.Vectors().CheckVectorSources(ctx, a, search.Profile, repository.SearchFilter{}, result.Sources), repository.ErrConflict)
	result, err = search.Search(ctx, a, "find", 10, repository.SearchFilter{})
	if err != nil || len(result.Emails) != 0 {
		t.Fatal("deleted source returned", err)
	}
}

func TestVectorSearchUnicodeSenderLiteral(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "unicode-sender")
	state, chunks, search := searchFixture(t, s, a, "one")
	stored, err := s.Emails().Get(ctx, a, state.EmailID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Email.Sender.Name = "Élodie Sender"
	saveEmail(t, s, stored.Email)
	stored, err = s.Emails().Get(ctx, a, state.EmailID)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []repository.ChunkInput{}
	for _, c := range chunks {
		inputs = append(inputs, c.ChunkInput)
	}
	input := repository.ChunkSnapshot{AccountID: a, EmailID: state.EmailID, SourceRevision: stored.ContentHash, PreprocessingVersion: state.PreprocessingVersion, IndexVersion: state.IndexVersion, Model: state.Model, Chunks: inputs}
	state, chunks = replaceChunks(t, s, input)
	readyChunks(t, s, state, chunks)
	for _, sender := range []string{"Élodie", "Élodie sender", "SENDER"} {
		result, err := search.Search(ctx, a, "find", 10, repository.SearchFilter{Sender: sender})
		if err != nil || len(result.Emails) != 1 {
			t.Fatalf("sender %q: %v %d", sender, err, len(result.Emails))
		}
	}
}
