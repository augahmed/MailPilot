package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
		if r.EmailID != row.EmailID {
			if r.State != "suggested" {
				t.Fatal("unrelated source invalidated assessment", r)
			}
			continue
		}
		if (r.State != "stale" && r.State != "not_reviewed") || len(r.Predictions) != 0 {
			t.Fatal("stale predictions visible", listed)
		}
	}
}

func TestOrganizationConfirmationSurvivesUnrelatedWorkAndIdenticalReassessment(t *testing.T) {
	for _, change := range []string{"reassessment", "other_source", "other_index"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			index, db, _, engine, _ := semanticAppFixture(t, 2)
			indexCommand(index, "start")
			if p := waitIndex(t, index); p.Status != "complete" {
				t.Fatal(p)
			}
			stored, err := db.Emails().Get(ctx, "a", "mail-000000")
			if err != nil {
				t.Fatal(err)
			}
			fake := &organizationFake{email: stored.Email, labels: []domain.EmailLabel{}}
			org := NewOrganization(ctx, index.session, func(domain.AccountID) OrganizationProvider { return fake }, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
			t.Cleanup(org.Close)
			preview := previewOrganization(t, org, stored.Email)
			original, err := db.Classifications().Get(ctx, "a", stored.Email.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "reassessment" {
				cats, err := db.Categories().List(ctx, "a")
				if err != nil {
					t.Fatal(err)
				}
				run, err := engine.Classify(ctx, stored.Email, cats)
				if err != nil {
					t.Fatal(err)
				}
				if run.InputHash != original.InputHash {
					t.Fatal("fixture evidence changed")
				}
				run.Timestamp = original.Timestamp.Add(time.Millisecond)
				for i := range run.Results {
					run.Results[i].Timestamp = run.Timestamp
				}
				if err := db.Classifications().Replace(ctx, run); err != nil {
					t.Fatal(err)
				}
			} else {
				other, err := db.Emails().Get(ctx, "a", "mail-000001")
				if err != nil {
					t.Fatal(err)
				}
				other.Email.BodyText += " Unrelated updated content."
				if _, err := db.Emails().Upsert(ctx, other.Email); err != nil {
					t.Fatal(err)
				}
				if change == "other_index" {
					chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
					if err != nil {
						t.Fatal(err)
					}
					if err := chunker.Prepare(ctx, "a", other.Email.ProviderID); err != nil {
						t.Fatal(err)
					}
				}
				current, err := db.Classifications().Get(ctx, "a", stored.Email.ID)
				if err != nil || current.InputHash != original.InputHash {
					t.Fatal("unrelated work revoked selected assessment", err)
				}
			}
			applied := applyOrganization(t, org, preview)
			if applied.Code != "" || applied.State != "applied" || len(fake.writes) != 2 {
				t.Fatalf("valid confirmation rejected: %+v", applied)
			}
		})
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

func TestSemanticMultipleSuggestionsSupportChoiceAndHighestModes(t *testing.T) {
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
	leader, err := db.Categories().Get(ctx, "a", domain.CategoryID(row.Predictions[0].CategoryID))
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Rows) != 1 || listed.Rows[0].Change != "multiple_suggestions" || listed.Rows[0].SuggestionCount != 6 || listed.Rows[0].ProposedLabel != "" {
		t.Fatal("default must retain multiple suggestions", listed)
	}
	defaultPreview := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{row.EmailID}})
	if defaultPreview.Code != "not_found" || len(fake.writes) != 0 {
		t.Fatal("default silently chose a category", defaultPreview)
	}
	options := org.Handle(ctx, OrganizationRequest{Operation: "suggestions", AccountID: "a", EmailIDs: []string{row.EmailID}})
	if options.Code != "" || len(options.Rows) != 1 || len(options.Rows[0].Suggestions) != 6 {
		t.Fatal("incomplete suggestion options", options)
	}
	other := org.Handle(ctx, OrganizationRequest{Operation: "suggestions", AccountID: "b", EmailIDs: []string{row.EmailID}})
	if other.Code != "unauthorized" || len(other.Rows) != 0 {
		t.Fatal("cross-account options exposed", other)
	}
	highestList := org.Handle(ctx, OrganizationRequest{Operation: "list", AccountID: "a", SuggestionMode: "highest"})
	if highestList.Rows[0].Change != "candidate" || highestList.Rows[0].ProposedLabel != "MailPilot/"+leader.Name {
		t.Fatal("highest mode did not use the leader", highestList)
	}
	highest := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", SuggestionMode: "highest", EmailIDs: []string{row.EmailID}})
	if highest.Code != "" || highest.Rows[0].ProposedLabel != "MailPilot/"+leader.Name || len(fake.writes) != 0 {
		t.Fatal("highest mode failed", highest)
	}
	forged := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{row.EmailID}, Choices: []OrganizationChoice{{EmailID: row.EmailID, CategoryID: "not-suggested"}}})
	if forged.Code != "not_found" || len(fake.writes) != 0 {
		t.Fatal("unsuggested category accepted", forged)
	}
	choice := options.Rows[0].Suggestions[2]
	preview := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{row.EmailID}, Choices: []OrganizationChoice{{EmailID: row.EmailID, CategoryID: choice.CategoryID}}})
	if preview.Code != "" || preview.State != "preview" || len(preview.Rows) != 1 || preview.Rows[0].ProposedLabel != "MailPilot/"+choice.Name || len(fake.writes) != 0 {
		t.Fatal("chosen automatic suggestion could not be previewed", preview)
	}

	current, err := db.Classifications().Get(ctx, "a", domain.EmailID(row.EmailID))
	if err != nil || len(current.Results) != 6 {
		t.Fatal("organization discarded classification suggestions", err)
	}
	snapshot, err := db.Semantic().Snapshot(ctx, "a", domain.EmailID(row.EmailID), s.profile)
	if err != nil || snapshot.Preference.Mode != "" {
		t.Fatal("preview manufactured a manual assignment", err)
	}
	plan, err := db.Organization().Get(ctx, "a", domain.ActionID(preview.ID))
	if err != nil || plan.Rows[0].Classification == nil || plan.Rows[0].InputHash != current.InputHash {
		t.Fatal("preview did not bind the complete automatic assessment", err)
	}
	applied := applyOrganization(t, org, preview)
	if applied.Code != "" || applied.State != "applied" || len(fake.writes) != 2 {
		t.Fatal("automatic suggestion was not applied after confirmation", applied)
	}
	assigned := review.Review(ctx, ClassificationRequest{Operation: "assigned", AccountID: "a", EmailID: row.EmailID, CategoryID: row.Predictions[len(row.Predictions)-1].CategoryID, Revision: row.ReaderRevision})
	if assigned.Errors != 0 || assigned.Rows[0].State != "assigned" || len(assigned.Rows[0].Predictions) != 1 || f.count() != calls {
		t.Fatal("manual primary failed or re-embedded mail", assigned)
	}
	listed = org.Handle(ctx, OrganizationRequest{Operation: "list", AccountID: "a"})
	override, err := db.Categories().Get(ctx, "a", domain.CategoryID(row.Predictions[len(row.Predictions)-1].CategoryID))
	if err != nil || listed.Rows[0].Change != "candidate" || listed.Rows[0].ProposedLabel != "MailPilot/"+override.Name || len(fake.writes) != 2 {
		t.Fatal("manual choice lost priority", listed, err)
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

func TestAutomaticOrganizationSuggestionSurvivesMetadataRefresh(t *testing.T) {
	ctx := context.Background()
	index, db, embedder, engine, _ := semanticAppFixture(t, 1)
	indexCommand(index, "start")
	if p := waitIndex(t, index); p.Status != "complete" {
		t.Fatal(p)
	}
	stored, err := db.Emails().Get(ctx, "a", "mail-000000")
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.Classifications().Get(ctx, "a", stored.Email.ID)
	if err != nil || len(run.Results) != 1 || run.Results[0].ScoreKind != "cosine_similarity" {
		t.Fatal("fixture lacks an automatic suggestion", err)
	}
	expected := run.Results[0].CategoryID
	// An opaque source-header refresh changes the original source hash while all
	// model inputs remain identical. No manual category preference is created.
	live := stored.Email
	live.Headers = append(append([]domain.EmailHeader{}, live.Headers...), domain.EmailHeader{Name: "X-Transport-Revision", Value: "new"})
	fake := &organizationFake{email: live, labels: []domain.EmailLabel{}}
	org := NewOrganization(ctx, index.session, func(domain.AccountID) OrganizationProvider { return fake }, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	org.SetChunkProcessor(chunker.Prepare)
	t.Cleanup(org.Close)
	calls := embedder.count()
	preview := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{string(stored.Email.ID)}})
	if preview.Code != "" || preview.State != "preview" || len(preview.Rows) != 1 || preview.Rows[0].ScoreKind != "cosine_similarity" {
		t.Fatalf("automatic suggestion rejected after metadata refresh: %+v", preview)
	}
	plan, err := db.Organization().Get(ctx, "a", domain.ActionID(preview.ID))
	if err != nil || plan.Rows[0].CategoryID != expected {
		t.Fatal("preview lost automatic category", err)
	}
	snapshot, err := db.Semantic().Snapshot(ctx, "a", stored.Email.ID, index.profile)
	if err != nil || snapshot.Preference.Mode != "" || snapshot.Email.ContentHash == stored.ContentHash || snapshot.Availability != "ready" {
		t.Fatal("automatic preview used a manual preference or stale vectors", err)
	}
	applied := applyOrganization(t, org, preview)
	if applied.Code != "" || applied.State != "applied" || len(fake.writes) != 2 {
		t.Fatal("automatic category could not be applied", applied)
	}
	if embedder.count() != calls {
		t.Fatal("automatic organization started inference")
	}
}

func TestOrganizationNeverCollapsesLegacyManualAssignments(t *testing.T) {
	ctx := context.Background()
	index, db, _, engine, _ := semanticAppFixture(t, 1)
	indexCommand(index, "start")
	waitIndex(t, index)
	cats, err := db.Categories().List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range cats[:2] {
		if err := db.Categories().Assign(ctx, "a", "mail-000000", cat.ID); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := db.Emails().Get(ctx, "a", "mail-000000")
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Classify(ctx, stored.Email, cats)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	fake := &organizationFake{email: stored.Email, labels: []domain.EmailLabel{}}
	org := NewOrganization(ctx, index.session, func(domain.AccountID) OrganizationProvider { return fake }, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
	t.Cleanup(org.Close)
	for _, mode := range []string{"choose", "highest"} {
		list := org.Handle(ctx, OrganizationRequest{Operation: "list", AccountID: "a", SuggestionMode: mode})
		if len(list.Rows) != 1 || list.Rows[0].Change != "multiple_suggestions" || list.Rows[0].SuggestionCount != 0 {
			t.Fatal("legacy assignments offered an automatic primary", list)
		}
		preview := awaitOrganization(t, org, OrganizationRequest{Operation: "preview", AccountID: "a", SuggestionMode: mode, EmailIDs: []string{string(stored.Email.ID)}})
		if preview.Code != "not_found" || len(fake.writes) != 0 {
			t.Fatal("legacy assignments were collapsed", preview)
		}
	}
	current, err := db.Emails().Get(ctx, "a", stored.Email.ID)
	if err != nil || len(current.Email.Categories) != 2 {
		t.Fatal("legacy assignments changed", err)
	}
}
