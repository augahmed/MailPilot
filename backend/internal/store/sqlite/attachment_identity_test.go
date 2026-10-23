package sqlite

import (
	"context"
	"errors"
	"testing"

	"mailpilot.local/core/internal/repository"
)

func TestAttachmentHandleRefreshPreservesAssessmentAndVectors(t *testing.T) {
	ctx := context.Background()
	s, run, engine, id := semanticStoreFixture(t)
	result := assessSemantic(t, s, run, engine, id)
	if err := s.Semantic().SaveIndexed(ctx, run, result); err != nil {
		t.Fatal(err)
	}
	before, err := s.Semantic().Snapshot(ctx, run.AccountID, id, run.Profile)
	if err != nil {
		t.Fatal(err)
	}
	e := before.Email.Email
	if len(e.Attachments) == 0 {
		t.Fatal("fixture requires attachment metadata")
	}
	e.Attachments[0].ProviderID = "another-opaque-download-handle"
	if _, err := s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	after, err := s.Semantic().Snapshot(ctx, run.AccountID, id, run.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash != before.Hash || after.Generation != before.Generation || after.Email.ContentHash != before.Email.ContentHash || len(after.Chunks) != len(before.Chunks) {
		t.Fatal("transport handle invalidated shared source/vector/assessment identities")
	}
	if _, err := s.Classifications().Get(ctx, run.AccountID, id); err != nil {
		t.Fatal("unchanged reviewed assessment became stale", err)
	}
	if err := s.Semantic().SaveIndexed(ctx, run, result); err != nil {
		t.Fatal("unchanged work was rejected", err)
	}
	// Actual attachment metadata still fences old semantic work and assessments.
	e.Attachments[0].Filename = "changed-evidence.pdf"
	if _, err := s.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.Semantic().SaveIndexed(ctx, run, result); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale worker accepted changed attachment metadata", err)
	}
	if _, err := s.Classifications().Get(ctx, run.AccountID, id); err == nil {
		t.Fatal("stale assessment survived a meaningful attachment change")
	}
}
