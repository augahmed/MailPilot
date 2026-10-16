package sqlite

import (
	"context"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func TestHistoricalSemanticAssessmentPersistence(t *testing.T) {
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
