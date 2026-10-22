package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

func semanticAppFixture(t *testing.T, n int) (*LocalIndex, *sqlite.Store, *indexFake, *classification.SemanticClassifier, *ClassificationReview) {
	t.Helper()
	s, db, f, _, _ := indexFixture(t, n)
	s.EnableCategoryVectors(db.CategoryVectors())
	f.invoke = func(_ context.Context, texts []string) ([][]float32, error) {
		out := fakeIndexVectors(texts, f.descriptor.Dimensions)
		for i, text := range texts {
			if !strings.Contains(text, "Finance") && !strings.Contains(text, "Project update") {
				out[i][0] = 0
				out[i][1] = 1
			}
		}
		return out, nil
	}
	engine, err := classification.NewSemanticClassifier(db.Semantic(), s.profile, nil, classification.DefaultSemanticPolicy())
	if err != nil {
		t.Fatal(err)
	}
	s.EnableClassification(db.Semantic(), engine)
	review := NewClassificationReview(s.session, engine, db.Emails(), db.Categories(), db.Classifications())
	review.EnableSemantic(db.Semantic(), db.Chunks())
	return s, db, f, engine, review
}
func TestSemanticCoordinatorAndReviewOverrides(t *testing.T) {
	ctx := context.Background()
	s, db, f, _, review := semanticAppFixture(t, 3)
	before := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if before.Rows[0].State != "not_reviewed" || f.count() != 0 {
		t.Fatal("list ran classification or inference")
	}
	indexCommand(s, "start")
	p := waitIndex(t, s)
	if p.Status != "complete" || p.AssessedMessages != 3 || p.PendingAssessments != 0 {
		t.Fatal("coordinator omitted assessments", p)
	}
	calls := f.count()
	page := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if page.Errors != 0 || page.Rows[0].State != "suggested" || page.Rows[0].Semantic == nil || len(page.Rows[0].Semantic.Scores[0].Evidence) < 2 {
		t.Fatal("missing review evidence", page)
	}
	row := page.Rows[0]
	cats, _ := db.Categories().List(ctx, "a")
	var personal domain.CategoryID
	for _, c := range cats {
		if c.Name == "Personal" {
			personal = c.ID
		}
	}
	assigned := review.Review(ctx, ClassificationRequest{Operation: "assigned", AccountID: "a", EmailID: row.EmailID, CategoryID: string(personal), Revision: row.ReaderRevision})
	if assigned.Errors != 0 || assigned.Rows[0].State != "assigned" || assigned.Rows[0].Predictions[0].CategoryID != string(personal) {
		t.Fatal("assignment failed", assigned)
	}
	source, _ := db.CategoryVectors().Source(ctx, "a", personal, s.profile)
	if len(source.Examples) != 0 {
		t.Fatal("correction became a training example")
	}
	abstained := review.Review(ctx, ClassificationRequest{Operation: "unclassified", AccountID: "a", EmailID: row.EmailID, Revision: row.ReaderRevision})
	if abstained.Rows[0].State != "unclassified" || len(abstained.Rows[0].Predictions) != 0 {
		t.Fatal("manual abstention lost", abstained)
	}
	automatic := review.Review(ctx, ClassificationRequest{Operation: "auto", AccountID: "a", EmailID: row.EmailID, Revision: row.ReaderRevision})
	if automatic.Rows[0].State != "suggested" || f.count() != calls {
		t.Fatal("automatic suggestion/inference reuse failed", automatic)
	}
	stored, _ := db.Emails().Get(ctx, "a", domain.EmailID(row.EmailID))
	if len(stored.Email.Labels) != 0 {
		t.Fatal("classification changed Gmail labels")
	}
	if p := review.Review(ctx, ClassificationRequest{Operation: "assigned", AccountID: "b", EmailID: row.EmailID, CategoryID: string(personal), Revision: row.ReaderRevision}); p.Code != "unauthorized" {
		t.Fatal("foreign override", p)
	}
	stored.Email.BodyText = "A changed source after the user saw the review."
	db.Emails().Upsert(ctx, stored.Email)
	if stale := review.Review(ctx, ClassificationRequest{Operation: "assigned", AccountID: "a", EmailID: row.EmailID, CategoryID: string(personal), Revision: row.ReaderRevision}); stale.Code != "conflict" {
		t.Fatal("stale confirmation accepted", stale)
	}
	listed := review.Review(ctx, ClassificationRequest{Operation: "list"})
	for _, r := range listed.Rows {
		if (r.State != "stale" && r.State != "not_reviewed") || len(r.Predictions) != 0 {
			t.Fatal("stale predictions visible", listed)
		}
	}
}

func TestSemanticReviewScoresForwardedMailWithoutAuthoredNote(t *testing.T) {
	ctx := context.Background()
	s, db, f, _, review := semanticAppFixture(t, 1)
	stored, err := db.Emails().Get(ctx, "a", "mail-000000")
	if err != nil {
		t.Fatal(err)
	}
	stored.Email.Subject = "Fwd: Project update"
	stored.Email.BodyText = "---------- Forwarded message ---------\nFrom: Colleague <colleague@example.test>\nSubject: Project update\n\nThe project review is scheduled for Friday. Please review the agenda."
	if _, err = db.Emails().Upsert(ctx, stored.Email); err != nil {
		t.Fatal(err)
	}
	indexCommand(s, "start")
	if p := waitIndex(t, s); p.Status != "complete" || p.AssessedMessages != 1 {
		t.Fatal(p)
	}
	calls := f.count()
	page := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if page.Errors != 0 || len(page.Rows) != 1 || page.Rows[0].Semantic == nil || len(page.Rows[0].Semantic.Scores) == 0 {
		t.Fatal("forwarded mail missing review scores", page)
	}
	forwarded := false
	for _, e := range page.Rows[0].Semantic.Scores[0].Evidence {
		forwarded = forwarded || e.Section == "forwarded"
	}
	if !forwarded || f.count() != calls {
		t.Fatal("review lost forwarded evidence or ran inference", page)
	}
}

func TestSemanticMultipleSuggestionsPersistAndRequirePrimaryForLabels(t *testing.T) {
	ctx := context.Background()
	s, db, f, engine, review := semanticAppFixture(t, 1)
	f.invoke = func(_ context.Context, texts []string) ([][]float32, error) {
		return fakeIndexVectors(texts, f.descriptor.Dimensions), nil
	}
	indexCommand(s, "start")
	if p := waitIndex(t, s); p.Status != "complete" {
		t.Fatal(p)
	}
	calls := f.count()
	page := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if page.Errors != 0 || len(page.Rows) != 1 || len(page.Rows[0].Predictions) != 6 || len(page.Rows[0].Semantic.Scores) != 6 || page.Rows[0].State != "suggested" {
		t.Fatal(page)
	}
	row := page.Rows[0]
	run, err := db.Classifications().Get(ctx, "a", domain.EmailID(row.EmailID))
	if err != nil || len(run.Results) != 6 {
		t.Fatal(run, err)
	}
	if _, err = db.Classifications().Get(ctx, "b", run.EmailID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account suggestions exposed", err)
	}
	run.Results = run.Results[:5]
	if err = db.Classifications().Replace(ctx, run); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("silently omitted close category", err)
	}
	stored, _ := db.Emails().Get(ctx, "a", domain.EmailID(row.EmailID))
	fake := &organizationFake{email: stored.Email, labels: []domain.EmailLabel{}}
	org := NewOrganization(ctx, s.session, func(domain.AccountID) OrganizationProvider { return fake }, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
	t.Cleanup(org.Close)
	listed := org.Handle(ctx, OrganizationRequest{Operation: "list", AccountID: "a"})
	if len(listed.Rows) != 1 || listed.Rows[0].Change != "multiple_suggestions" || listed.Rows[0].ProposedLabel != "" {
		t.Fatal(listed)
	}
	org.Handle(ctx, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{row.EmailID}})
	preview := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if preview.Code == "" || len(fake.writes) != 0 {
		t.Fatal("ambiguous suggestions prepared a label", preview, fake.writes)
	}
	assigned := review.Review(ctx, ClassificationRequest{Operation: "assigned", AccountID: "a", EmailID: row.EmailID, CategoryID: row.Predictions[0].CategoryID, Revision: row.ReaderRevision})
	if assigned.Errors != 0 || assigned.Rows[0].State != "assigned" || len(assigned.Rows[0].Predictions) != 1 || f.count() != calls {
		t.Fatal("manual primary failed or re-embedded mail", assigned)
	}
	listed = org.Handle(ctx, OrganizationRequest{Operation: "list", AccountID: "a"})
	if listed.Rows[0].Change != "candidate" || len(fake.writes) != 0 {
		t.Fatal(listed)
	}
}

type blockingAssessment struct {
	repository.SemanticRepository
	entered chan struct{}
	release chan struct{}
}

func (r *blockingAssessment) SaveIndexed(ctx context.Context, run repository.IndexRun, result domain.ClassificationRun) error {
	close(r.entered)
	select {
	case <-r.release:
		return r.SemanticRepository.SaveIndexed(ctx, run, result)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestSemanticCoordinatorPauseAndResumeReusesVectors(t *testing.T) {
	ctx := context.Background()
	// Cross a candidate-page boundary so cancellation also protects pending
	// assessments already returned alongside the blocked first publication.
	s, db, f, engine, _ := semanticAppFixture(t, 35)
	block := &blockingAssessment{SemanticRepository: db.Semantic(), entered: make(chan struct{}), release: make(chan struct{})}
	s.EnableClassification(block, engine)
	indexCommand(s, "start")
	<-block.entered
	calls := f.count()
	if p := indexCommand(s, "pause"); p.Status != "paused" {
		t.Fatal(p)
	}
	waitIndex(t, s)
	if _, err := db.Classifications().Get(ctx, "a", "mail-000000"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cancelled assessment published", err)
	}
	// Configure only after the prior worker has joined, as composition would do.
	<-s.worker.done
	s.EnableClassification(db.Semantic(), engine)
	indexCommand(s, "resume")
	p := waitIndex(t, s)
	if p.Status != "complete" || p.AssessedMessages != 35 || f.count() != calls {
		t.Fatal("resume repeated inference", p)
	}
}
func TestSemanticOrganizationBindsEvidenceAndPreservesUndo(t *testing.T) {
	ctx := context.Background()
	s, db, _, engine, review := semanticAppFixture(t, 1)
	indexCommand(s, "start")
	waitIndex(t, s)
	stored, _ := db.Emails().Get(ctx, "a", "mail-000000")
	fake := &organizationFake{email: stored.Email, labels: []domain.EmailLabel{}}
	org := NewOrganization(ctx, s.session, func(domain.AccountID) OrganizationProvider { return fake }, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
	t.Cleanup(org.Close)
	req := OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{string(stored.Email.ID)}}
	org.Handle(ctx, req)
	preview := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if preview.Code != "" || preview.State != "preview" || preview.Rows[0].ScoreKind != "cosine_similarity" || len(fake.writes) != 0 {
		t.Fatal("unsafe semantic preview", preview)
	}
	plan, err := db.Organization().Get(ctx, "a", domain.ActionID(preview.ID))
	if err != nil || plan.Rows[0].Classification == nil {
		t.Fatal("missing versioned journal evidence", err)
	}
	cats, _ := db.Categories().List(ctx, "a")
	cats[0].Description = "Edited definition after preview."
	db.Categories().Save(ctx, "a", cats[0])
	org.Handle(ctx, OrganizationRequest{Operation: "apply", AccountID: "a", ActionID: preview.ID, Digest: preview.Digest})
	stale := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if stale.Code != "stale_preview" || len(fake.writes) != 0 {
		t.Fatal("stale semantic preview applied", stale)
	}
	// Regenerate compatible prototypes and reassess through the same coordinator.
	indexCommand(s, "resume")
	waitIndex(t, s)
	review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	org.Handle(ctx, req)
	fresh := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if fresh.Code != "" {
		t.Fatal(fresh)
	}
	org.Handle(ctx, OrganizationRequest{Operation: "apply", AccountID: "a", ActionID: fresh.ID, Digest: fresh.Digest})
	applied := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if applied.State != "applied" || len(fake.writes) != 2 {
		t.Fatal("semantic label flow failed", applied, fake.writes)
	}
	org.Handle(ctx, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	undo := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if undo.Code != "" || undo.Kind != "undo" {
		t.Fatal("undo preview failed", undo)
	}
	org.Handle(ctx, OrganizationRequest{Operation: "apply", AccountID: "a", ActionID: undo.ID, Digest: undo.Digest})
	undone := awaitOrganization(t, org, OrganizationRequest{Operation: "status", AccountID: "a"})
	if undone.State != "applied" || len(fake.writes) != 3 {
		t.Fatal("semantic undo failed", undone, fake.writes)
	}
}

func TestSemanticReviewWithoutEmbeddingsAndModelProfileChange(t *testing.T) {
	ctx := context.Background()
	s, db, f, engine, review := semanticAppFixture(t, 1)
	page := review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	if page.Errors != 0 || page.Rows[0].State != "unclassified" || f.count() != 0 {
		t.Fatal("missing embeddings triggered inference or forced a category", page)
	}
	indexCommand(s, "start")
	waitIndex(t, s)
	calls := f.count()
	p := s.profile
	p.Model.Version = "unqualified-next-model"
	next, err := classification.NewSemanticClassifier(db.Semantic(), p, nil, classification.DefaultSemanticPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if next.Version() == engine.Version() {
		t.Fatal("classifier freshness omitted active model profile")
	}
	changed := NewClassificationReview(s.session, next, db.Emails(), db.Categories(), db.Classifications())
	changed.EnableSemantic(db.Semantic(), db.Chunks())
	listed := changed.Review(ctx, ClassificationRequest{Operation: "list"})
	if listed.Rows[0].State != "stale" || len(listed.Rows[0].Predictions) != 0 {
		t.Fatal("old model prediction remained current", listed)
	}
	assessed := changed.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	if assessed.Errors != 0 || assessed.Rows[0].State != "unclassified" || f.count() != calls {
		t.Fatal("untuned model inferred or reused semantic thresholds", assessed)
	}
}
