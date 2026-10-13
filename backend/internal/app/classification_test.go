package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

func reviewFixture(t *testing.T) (*sqlite.Store, *ClassificationReview, domain.Email, domain.Email) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	account, err := store.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "fixture", ProviderAccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := classification.NewRuleBasedClassifier(classification.StarterConfig())
	if err != nil {
		t.Fatal(err)
	}
	review := NewClassificationReview(func() (domain.AccountID, context.Context, error) { return account, ctx, nil }, engine, store.Emails(), store.Categories(), store.Classifications())
	receipt := domain.Email{AccountID: account, ProviderID: "receipt", Sender: domain.EmailAddress{Address: "store@example.test"}, Subject: "Your receipt", BodyText: "Payment received. Order number: FIXTURE-1.", BodyState: "complete", ReceivedAt: time.Unix(2, 0)}
	receipt.ID, err = store.Emails().Upsert(ctx, receipt)
	if err != nil {
		t.Fatal(err)
	}
	personal := domain.Email{AccountID: account, ProviderID: "personal", Sender: domain.EmailAddress{Address: "friend@example.test"}, Subject: "Saturday plans", BodyText: "Lunch at noon?", BodyState: "complete", ReceivedAt: time.Unix(1, 0)}
	personal.ID, err = store.Emails().Upsert(ctx, personal)
	if err != nil {
		t.Fatal(err)
	}
	return store, review, receipt, personal
}
func TestReviewRunsPersistsAndAbstainsWithoutAssignments(t *testing.T) {
	store, review, receipt, personal := reviewFixture(t)
	ctx := context.Background()
	before := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if before.Code != "" || len(before.Rows) != 2 || before.Rows[0].State != "not_reviewed" {
		t.Fatal("incorrect initial review")
	}
	page := review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	if page.Processed != 2 || page.Errors != 0 || len(page.Rows) != 2 || page.Rows[0].State != "classified" || page.Rows[1].State != "unclassified" {
		t.Fatalf("incorrect review: %+v", page)
	}
	prediction := page.Rows[0].Predictions[0]
	if prediction.EmailID != string(receipt.ID) || prediction.CategoryName != "Receipts" || prediction.Confidence != .6 || len(prediction.Reasons) != 3 || prediction.Timestamp == 0 {
		t.Fatal("missing review details")
	}
	for _, email := range []domain.Email{receipt, personal} {
		stored, err := store.Emails().Get(ctx, "a", email.ID)
		if err != nil || len(stored.Email.Categories) != 0 || len(stored.Email.Labels) != 0 {
			t.Fatal("classification changed membership", err)
		}
	}
	saved := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if saved.Processed != 0 || saved.Rows[0].Predictions[0].Timestamp != prediction.Timestamp || saved.Rows[1].Timestamp == nil {
		t.Fatal("read reran classifier or lost abstention")
	}
	// A label change makes the saved assessment stale even when the email body is identical.
	receipt.Labels = []domain.EmailLabel{{AccountID: "a", ProviderID: "opaque", Name: "Other", Kind: domain.LabelKindUser}}
	if _, err := store.Emails().Upsert(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	saved = review.Review(ctx, ClassificationRequest{Operation: "list"})
	if saved.Rows[0].State != "stale" || len(saved.Rows[0].Predictions) != 0 {
		t.Fatal("stale prediction displayed")
	}
}
func TestReviewAuthorizationAndValidation(t *testing.T) {
	store, review, receipt, _ := reviewFixture(t)
	for _, p := range []ClassificationRequest{{Operation: "run"}, {Operation: "apply", AccountID: "a"}, {Operation: "list", Offset: -1}, {Operation: "run", AccountID: "a", Offset: 1000001}} {
		page := review.Review(context.Background(), p)
		if page.Code != "validation" || len(page.Rows) != 0 {
			t.Fatal("invalid request accepted")
		}
	}
	page := review.Review(context.Background(), ClassificationRequest{Operation: "run", AccountID: "other"})
	if page.Code != "unauthorized" || page.Processed != 0 || len(page.Rows) != 0 {
		t.Fatal("cross-account review accepted")
	}
	if _, err := store.Classifications().Get(context.Background(), "a", receipt.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("unauthorized request persisted a result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	review.session = func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }
	if page = review.Review(context.Background(), ClassificationRequest{Operation: "list"}); page.Code != "unauthorized" {
		t.Fatal("cancelled session accepted")
	}
}

type failResults struct {
	repository.ClassificationRepository
	email domain.EmailID
}

func (f failResults) Replace(ctx context.Context, run domain.ClassificationRun) error {
	if run.EmailID == f.email {
		return repository.ErrStorage
	}
	return f.ClassificationRepository.Replace(ctx, run)
}
func TestReviewReportsPartialStorageFailures(t *testing.T) {
	store, review, receipt, personal := reviewFixture(t)
	review.results = failResults{store.Classifications(), personal.ID}
	page := review.Review(context.Background(), ClassificationRequest{Operation: "run", AccountID: "a"})
	if page.Processed != 1 || page.Errors != 1 || page.Rows[1].State != "error" || page.Rows[1].Code != "storage_error" {
		t.Fatal("partial result hidden")
	}
	if _, err := store.Classifications().Get(context.Background(), "a", receipt.ID); err != nil {
		t.Fatal("successful classification lost", err)
	}
}
func TestReviewHidesOldClassifierVersionsAndPages(t *testing.T) {
	store, review, _, _ := reviewFixture(t)
	ctx := context.Background()
	review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	config := classification.StarterConfig()
	config.Version = "starter-2"
	review.engine, _ = classification.NewRuleBasedClassifier(config)
	page := review.Review(ctx, ClassificationRequest{Operation: "list"})
	if page.Rows[0].State != "stale" || page.Rows[1].State != "stale" {
		t.Fatal("old version shown as current")
	}
	for i := 0; i < 4; i++ {
		_, err := store.Emails().Upsert(ctx, domain.Email{AccountID: "a", ProviderID: domain.ProviderMessageID(string(rune('A' + i))), ReceivedAt: time.Unix(int64(3+i), 0)})
		if err != nil {
			t.Fatal(err)
		}
	}
	page = review.Review(ctx, ClassificationRequest{Operation: "list"})
	if len(page.Rows) != 5 || !page.HasMore {
		t.Fatal("review page unbounded")
	}
	page = review.Review(ctx, ClassificationRequest{Operation: "list", Offset: 5})
	if len(page.Rows) != 1 || page.HasMore {
		t.Fatal("incorrect second page")
	}
}

type wrongAccountEngine struct {
	classification.ClassificationEngine
}

func (e wrongAccountEngine) Classify(ctx context.Context, email domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	run, err := e.ClassificationEngine.Classify(ctx, email, categories)
	run.AccountID = "other"
	return run, err
}
func TestReviewRejectsClassifierIdentityChanges(t *testing.T) {
	store, review, receipt, _ := reviewFixture(t)
	review.engine = wrongAccountEngine{review.engine}
	page := review.Review(context.Background(), ClassificationRequest{Operation: "run", AccountID: "a"})
	if page.Processed != 0 || page.Errors != 2 || page.Rows[0].Code != "invalid_message" {
		t.Fatal("classifier output exceeded account authority")
	}
	if _, err := store.Classifications().Get(context.Background(), "a", receipt.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("untrusted classifier output persisted")
	}
}

func TestClassificationResponseFitsPrivateFrameWithEscapedText(t *testing.T) {
	// Worst-case JSON escaping must still fit the host/core frame. Display values
	// are rune-bounded; opaque metadata/reasons are byte-bounded.
	text := func(n int) string { return "x" + strings.Repeat("\x01", n-1) }
	page := ClassificationPage{AccountID: text(100), Rows: []ClassificationRow{}}
	timestamp := time.Now().UnixMilli()
	for i := 0; i < 5; i++ {
		prediction := ClassificationDTO{EmailID: text(100), CategoryID: text(100), CategoryName: text(101), Confidence: .6, ClassifierName: text(100), ClassifierVersion: text(100), Timestamp: timestamp, Reasons: []string{}}
		for j := 0; j < 6; j++ {
			prediction.Reasons = append(prediction.Reasons, text(160))
		}
		assessment := &ClassificationAssessmentDTO{UsedClassifier: text(100), Reason: text(160), Candidates: []ClassificationCandidateDTO{}}
		for j := 0; j < 5; j++ {
			assessment.Candidates = append(assessment.Candidates, ClassificationCandidateDTO{CategoryID: text(100), CategoryName: text(101), Similarity: .6})
		}
		page.Rows = append(page.Rows, ClassificationRow{Assessment: assessment, EmailID: prediction.EmailID, Sender: text(101), Subject: text(201), BodyState: "complete", State: "classified", Timestamp: &timestamp, Predictions: []ClassificationDTO{prediction}})
	}
	encoded, err := json.Marshal(struct {
		JSONRPC, ID string
		Result      ClassificationPage
	}{"2.0", strings.Repeat("i", 64), boundedClassificationPage(page)})
	if err != nil || len(encoded)+1 >= 64*1024 {
		t.Fatal("classification DTO exceeds private frame limit", len(encoded), err)
	}
}

type reviewSemanticEmbeddings struct{ err error }

func (e *reviewSemanticEmbeddings) ProviderName() string { return "fixture/model" }
func (e *reviewSemanticEmbeddings) Dimension() int       { return 2 }
func (e *reviewSemanticEmbeddings) EmbedEmail(context.Context, domain.AccountID, domain.EmailID) ([]float32, error) {
	return []float32{1, 0}, e.err
}
func (e *reviewSemanticEmbeddings) EmbedCategories(_ context.Context, _ domain.AccountID, categories []domain.EmailCategory) ([][]float32, error) {
	v := make([][]float32, len(categories))
	for i, c := range categories {
		if c.Name == "Receipts" {
			v[i] = []float32{1, 0}
		} else {
			v[i] = []float32{0, 1}
		}
	}
	return v, e.err
}
func TestHybridReviewStoresAndExposesSemanticEvidence(t *testing.T) {
	store, review, receipt, _ := reviewFixture(t)
	fake := &reviewSemanticEmbeddings{}
	semantic, err := classification.NewSemanticClassifier(fake, classification.DefaultSemanticConfig())
	if err != nil {
		t.Fatal(err)
	}
	hybrid, err := classification.NewHybridClassifier(review.engine, semantic, classification.DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	review.engine = hybrid
	request := ClassificationRequest{Operation: "run", AccountID: "a"}
	page := review.Review(context.Background(), request)
	if page.Errors != 0 || page.Processed != 2 || page.Rows[0].Assessment == nil || page.Rows[0].Assessment.UsedClassifier != "HybridClassifier" || len(page.Rows[0].Assessment.Candidates) != 3 || page.Rows[0].Predictions[0].ScoreKind != "cosine_similarity" {
		t.Fatal(page)
	}
	old, err := store.Classifications().Get(context.Background(), "a", receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake.err = embedding.ErrProvider
	failed := review.Review(context.Background(), request)
	if failed.Errors != 2 || failed.Rows[0].Code != "embedding_unavailable" {
		t.Fatal(failed)
	}
	saved, err := store.Classifications().Get(context.Background(), "a", receipt.ID)
	if err != nil || !saved.Timestamp.Equal(old.Timestamp) {
		t.Fatal("failed inference overwrote prior assessment", err)
	}
}
