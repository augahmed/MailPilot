package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

func cachedChunkFixture(t *testing.T) (*sqlite.Store, *CachedChunker, domain.Email) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, id := range []domain.AccountID{"a", "b"} {
		if _, err = s.Accounts().Upsert(ctx, domain.Account{ID: id, Provider: "fixture", ProviderAccountID: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	html := `<p>Available original body.</p><blockquote>Quoted history.</blockquote>`
	e := domain.Email{ID: "email", AccountID: "a", ProviderID: "provider", Subject: "Cached project", BodyText: "Available original body.\r\n\n> Quoted history.", BodyHTML: &html, BodyState: "complete", ReceivedAt: time.Unix(1700000000, 0)}
	if _, err = s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	chunker, err := NewCachedChunker(s.Emails(), s.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	return s, chunker, e
}
func TestCachedChunkingPersistsWithoutEmbeddingOrChangingSource(t *testing.T) {
	ctx := context.Background()
	s, chunker, e := cachedChunkFixture(t)
	before, err := s.Emails().Get(ctx, e.AccountID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = chunker.Prepare(ctx, e.AccountID, e.ProviderID); err != nil {
		t.Fatal(err)
	}
	state, err := s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil || state.Status != "pending" || state.ContentStatus != "complete" || state.LastIndexedAt != nil {
		t.Fatal("claimed embeddings or incorrect coverage", err)
	}
	chunks, err := s.Chunks().List(ctx, e.AccountID, e.ID, repository.Page{Limit: 100})
	if err != nil || len(chunks) < 2 {
		t.Fatal("no persisted chunks", err)
	}
	for _, c := range chunks {
		embedding, err := s.Chunks().Embedding(ctx, e.AccountID, e.ID, c.ID, mailtext.ChunkModel())
		if err != nil || embedding.Status != "pending" || len(embedding.Vector) != 0 || c.SourceKind == "" || c.ContextText == "" {
			t.Fatal("bad persisted provenance/pending vector", err)
		}
	}
	after, err := s.Emails().Get(ctx, e.AccountID, e.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("chunking rewrote the cached message", err)
	}
	if _, err = s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err = chunker.Prepare(ctx, e.AccountID, e.ProviderID); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil || !reflect.DeepEqual(state, unchanged) {
		t.Fatal("unchanged reimport regenerated state", err)
	}
	again, err := s.Chunks().List(ctx, e.AccountID, e.ID, repository.Page{Limit: 100})
	if err != nil || !reflect.DeepEqual(chunks, again) {
		t.Fatal("unchanged reimport regenerated chunks", err)
	}
	requireChunkError(t, chunker.Prepare(ctx, "b", e.ProviderID), repository.ErrNotFound)
}
func requireChunkError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error %v, want %v", got, want)
	}
}
func TestCachedChunkCoverageAndRejectedSource(t *testing.T) {
	ctx := context.Background()
	s, chunker, e := cachedChunkFixture(t)
	e.BodyText = ""
	e.BodyHTML = nil
	e.BodyState = "complete"
	if _, err := s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := chunker.Prepare(ctx, e.AccountID, e.ProviderID); err != nil {
		t.Fatal(err)
	}
	state, err := s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil || state.Status != "partial" || state.ContentStatus != "unavailable" || state.BodyState != "complete" {
		t.Fatal("missing body was claimed complete", err)
	}
	// Body cache remains independent of normalization failures and never loses data.
	e.BodyText = strings.Repeat("x", mailtext.MaxBodyBytes+1)
	if _, err = s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err = chunker.Prepare(ctx, e.AccountID, e.ProviderID); err == nil {
		t.Fatal("oversized body accepted")
	}
	state, err = s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil || state.Status != "stale" {
		t.Fatal("failed preparation revived old chunks", err)
	}
	stored, err := s.Emails().Get(ctx, e.AccountID, e.ID)
	if err != nil || stored.Email.BodyText != e.BodyText {
		t.Fatal("failed preparation altered cached source", err)
	}
}

type changedChunkSource struct {
	repository.ChunkRepository
	before func()
}

func (r changedChunkSource) Replace(ctx context.Context, s repository.ChunkSnapshot) (repository.IndexState, error) {
	r.before()
	return r.ChunkRepository.Replace(ctx, s)
}
func TestCachedChunkingRejectsChangedSourceAndCancellation(t *testing.T) {
	ctx := context.Background()
	s, _, e := cachedChunkFixture(t)
	changed := e
	changed.BodyText = "Source changed while projection was computed."
	chunker, err := NewCachedChunker(s.Emails(), changedChunkSource{s.Chunks(), func() {
		if _, err := s.Emails().Upsert(ctx, changed); err != nil {
			t.Fatal(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	requireChunkError(t, chunker.Prepare(ctx, e.AccountID, e.ProviderID), repository.ErrConflict)
	_, err = s.Chunks().State(ctx, e.AccountID, e.ID)
	requireChunkError(t, err, repository.ErrNotFound)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	requireChunkError(t, chunker.Prepare(canceled, e.AccountID, e.ProviderID), context.Canceled)
}
func TestCachedBackfillIsBoundedAndResumes(t *testing.T) {
	ctx := context.Background()
	s, chunker, e := cachedChunkFixture(t)
	ids := []domain.EmailID{e.ID}
	for _, id := range []string{"second", "third"} {
		copy := e
		copy.ID = domain.EmailID(id)
		copy.ProviderID = domain.ProviderMessageID(id)
		copy.ReceivedAt = copy.ReceivedAt.Add(time.Second)
		if _, err := s.Emails().Upsert(ctx, copy); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, copy.ID)
	}
	foreign := e
	foreign.AccountID = "b"
	foreign.ID = "foreign"
	if _, err := s.Emails().Upsert(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		failed, err := chunker.PrepareCached(ctx, e.AccountID, 1)
		if err != nil || failed != 0 {
			t.Fatal("backfill failed", err)
		}
		count := 0
		for _, id := range ids {
			if _, err = s.Chunks().State(ctx, e.AccountID, id); err == nil {
				count++
			}
		}
		if count != want {
			t.Fatal("backfill did not respect limit/resume", count, want)
		}
	}
	_, err := s.Chunks().State(ctx, foreign.AccountID, foreign.ID)
	requireChunkError(t, err, repository.ErrNotFound)
	state, err := s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := chunker.PrepareCached(ctx, e.AccountID, 1)
	if err != nil || failed != 0 {
		t.Fatal(err)
	}
	again, err := s.Chunks().State(ctx, e.AccountID, e.ID)
	if err != nil || !reflect.DeepEqual(state, again) {
		t.Fatal("fresh backfill regenerated state", err)
	}
}
