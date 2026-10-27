package sqlite

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"strings"
	"testing"
)

func TestOnlyValidatedDemoProfileCanStartSyntheticIndex(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	const id domain.AccountID = "mailpilot-synthetic-demo-v1"
	if _, err := s.Accounts().Upsert(ctx, domain.Account{ID: id, Provider: "synthetic", ProviderAccountID: string(id)}); err != nil {
		t.Fatal(err)
	}
	p := repository.IndexProfile{Model: repository.EmbeddingModel{ID: "fixture-model", Version: "v1", Dimension: 2}, PreprocessingVersion: "fixture-text", IndexVersion: "fixture-index", Fingerprint: strings.Repeat("a", 64)}
	if _, err := s.Indexing().Begin(ctx, id, p, false); err == nil {
		t.Fatal("unvalidated synthetic account started indexing")
	}
	if err := s.VerifyProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Indexing().Begin(ctx, id, p, false); err != nil {
		t.Fatal("validated demo could not index", err)
	}
	if _, err := s.Accounts().Upsert(ctx, domain.Account{ID: "other", Provider: "synthetic", ProviderAccountID: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Indexing().Begin(ctx, "other", p, false); err == nil {
		t.Fatal("unrelated synthetic account gained index authority")
	}
	if err := s.VerifyProfile(ctx, true); err == nil {
		t.Fatal("mixed demo profile was accepted")
	}
	if _, err := s.Indexing().Begin(ctx, id, p, false); err == nil {
		t.Fatal("failed validation retained demo index authority")
	}
}
