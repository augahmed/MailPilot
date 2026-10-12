package sqlite

import (
	"context"
	"errors"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

func TestCategoryEmbeddingCacheAndIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "category-vector-a")
	b := account(t, s, "category-vector-b")
	c := domain.EmailCategory{Name: "Synthetic", Description: "Synthetic category description", Enabled: true}
	var err error
	c.ID, err = s.Categories().Save(ctx, a, c)
	if err != nil {
		t.Fatal(err)
	}
	p := &fixtureEmbedder{}
	service, err := embedding.NewLocalClassificationService(p, s.Emails(), s.Vectors(), s.CategoryVectors())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = service.EmbedCategories(ctx, a, []domain.EmailCategory{c}); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != 1 {
		t.Fatal("unchanged categories re-embedded")
	}
	prepared, _ := embedding.PrepareCategory(c)
	key := embedding.CategoryKey{AccountID: b, CategoryID: c.ID, Provider: p.ProviderName(), Dimension: 2, Version: embedding.CategoryPreprocessingVersion, InputHash: prepared.Hash}
	if _, err := s.CategoryVectors().GetCategory(ctx, key); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account vector", err)
	}
	if _, err := service.EmbedCategories(ctx, b, []domain.EmailCategory{c}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account save", err)
	}
	stale := c
	c.Description = "Changed description"
	if _, err = s.Categories().Save(ctx, a, c); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "category_embeddings") != 0 {
		t.Fatal("changed description retained vector")
	}
	if _, err := service.EmbedCategories(ctx, a, []domain.EmailCategory{stale}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale category saved", err)
	}
	if _, err := service.EmbedCategories(ctx, a, []domain.EmailCategory{c}); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "category_embeddings") != 1 {
		t.Fatal("missing category vector")
	}
	c.Enabled = false
	if _, err = s.Categories().Save(ctx, a, c); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "category_embeddings") != 0 {
		t.Fatal("disabled category vector retained")
	}
}
func TestSemanticAssessmentPersistence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "semantic-store")
	b := account(t, s, "other-account")
	id := saveEmail(t, s, sampleEmail(a, "one"))
	cats, err := s.Categories().List(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	run := assessment(t, s, a, id, cats[0].ID)
	run.UsedClassifier = "SemanticClassifier"
	run.Reason = "Synthetic semantic reason."
	run.Results[0].ScoreKind = "cosine_similarity"
	run.Results[0].Confidence = .8
	run.Candidates = []domain.ClassificationCandidate{{CategoryID: cats[0].ID, Similarity: .8}, {CategoryID: cats[1].ID, Similarity: .4}}
	if err = s.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := s.Classifications().Get(ctx, a, id)
	if err != nil || got.UsedClassifier != run.UsedClassifier || got.Results[0].ScoreKind != "cosine_similarity" || len(got.Candidates) != 2 {
		t.Fatal(got, err)
	}
	bad := run
	bad.Candidates = append([]domain.ClassificationCandidate{}, run.Candidates...)
	bad.Candidates[0].Similarity = .7
	if err = s.Classifications().Replace(ctx, bad); err != repository.ErrInvalid {
		t.Fatal("inconsistent selected score", err)
	}
	foreign, err := s.Categories().List(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	bad = run
	bad.Candidates = append([]domain.ClassificationCandidate{}, run.Candidates...)
	bad.Candidates[1].CategoryID = foreign[0].ID
	if err = s.Classifications().Replace(ctx, bad); err != repository.ErrConflict {
		t.Fatal("foreign candidate accepted", err)
	}
	cat := cats[1]
	cat.Description = "Modified definition"
	if _, err = s.Categories().Save(ctx, a, cat); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Classifications().Get(ctx, a, id); err != repository.ErrConflict {
		t.Fatal("stale semantic candidates visible", err)
	}
}
