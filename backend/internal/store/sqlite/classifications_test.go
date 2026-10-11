package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func runWithResult(result domain.ClassificationResult) domain.ClassificationRun {
	return domain.ClassificationRun{AccountID: result.AccountID, EmailID: result.EmailID, InputHash: result.InputHash, ClassifierName: result.ClassifierName, ClassifierVersion: result.ClassifierVersion, Timestamp: result.Timestamp, Results: []domain.ClassificationResult{result}}
}
func assessment(t *testing.T, s *Store, a domain.AccountID, email domain.EmailID, category domain.CategoryID) domain.ClassificationRun {
	t.Helper()
	ctx := context.Background()
	stored, err := s.Emails().Get(ctx, a, email)
	if err != nil {
		t.Fatal(err)
	}
	categories, err := s.Categories().List(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.ClassificationRun{AccountID: a, EmailID: email, ClassifierName: "fixture", ClassifierVersion: "1", InputHash: classification.InputHash(stored.Email, categories), Timestamp: time.Now().UTC().Truncate(time.Millisecond), Results: []domain.ClassificationResult{}}
	if category != "" {
		run.Results = append(run.Results, domain.ClassificationResult{AccountID: a, EmailID: email, CategoryID: category, Confidence: .6, ClassifierName: run.ClassifierName, ClassifierVersion: run.ClassifierVersion, InputHash: run.InputHash, Reasons: []string{"Synthetic subject and body evidence."}, Timestamp: run.Timestamp})
	}
	return run
}
func TestClassificationPersistenceAndAtomicReplacement(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "a")
	b := account(t, s, "b")
	email := saveEmail(t, s, sampleEmail(a, "mail"))
	defaults, _ := s.Categories().List(ctx, a)
	run := assessment(t, s, a, email, defaults[0].ID)
	for i := 0; i < 2; i++ {
		if err := s.Classifications().Replace(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Classifications().Get(ctx, a, email)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatal("classification round trip", err)
	}
	if count(t, s, "classification_results") != 1 || count(t, s, "classification_runs") != 1 {
		t.Fatal("replay duplicated assessments")
	}
	if _, err = s.Classifications().Get(ctx, b, email); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign classification returned")
	}
	bad := run
	bad.AccountID = b
	if err = s.Classifications().Replace(ctx, bad); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("mismatched result identity accepted")
	}
	// Fail after updating the run and deleting old predictions to prove rollback.
	if _, err = s.db.Exec(`CREATE TEMP TRIGGER fixture_classification_failure BEFORE INSERT ON classification_results WHEN NEW.classifier_version='fixture-failure' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	bad = run
	bad.ClassifierVersion = "fixture-failure"
	bad.Results = append([]domain.ClassificationResult{}, run.Results...)
	bad.Results[0].ClassifierVersion = bad.ClassifierVersion
	if err = s.Classifications().Replace(ctx, bad); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("injected write failure", err)
	}
	got, _ = s.Classifications().Get(ctx, a, email)
	if !reflect.DeepEqual(got, run) {
		t.Fatal("failed replacement partially persisted")
	}
	older := run
	older.Timestamp = run.Timestamp.Add(-time.Second)
	older.Results = append([]domain.ClassificationResult{}, run.Results...)
	older.Results[0].Timestamp = older.Timestamp
	if err = s.Classifications().Replace(ctx, older); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("older assessment overwrote newer")
	}
	empty := run
	empty.Results = []domain.ClassificationResult{}
	empty.Timestamp = run.Timestamp.Add(time.Millisecond)
	if err = s.Classifications().Replace(ctx, empty); err != nil {
		t.Fatal(err)
	}
	got, err = s.Classifications().Get(ctx, a, email)
	if err != nil || len(got.Results) != 0 || count(t, s, "classification_results") != 0 {
		t.Fatal("abstention retained old prediction", err)
	}
}
func TestClassificationRejectsStaleLabelsAndCategoryDefinitions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "a")
	emailValue := sampleEmail(a, "mail")
	id := saveEmail(t, s, emailValue)
	categories, _ := s.Categories().List(ctx, a)
	run := assessment(t, s, a, id, categories[0].ID)
	if err := s.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	emailValue.Labels = nil
	saveEmail(t, s, emailValue)
	if _, err := s.Classifications().Get(ctx, a, id); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("label-dependent assessment remained current")
	}
	if err := s.Classifications().Replace(ctx, run); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale labels accepted")
	}
	run = assessment(t, s, a, id, categories[0].ID)
	if err := s.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	category := categories[0]
	category.Enabled = false
	if _, err := s.Categories().Save(ctx, a, category); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Classifications().Get(ctx, a, id); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("disabled category remained current")
	}
	if err := s.Categories().Delete(ctx, a, category.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("disabling erased reference protection")
	}
	if err := s.Classifications().Replace(ctx, run); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("changed definition accepted")
	}
	current := assessment(t, s, a, id, category.ID)
	if err := s.Classifications().Replace(ctx, current); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("disabled category predicted")
	}
}
func TestClassificationValidatesReasonsAndConfidence(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "a")
	email := saveEmail(t, s, sampleEmail(a, "mail"))
	categories, _ := s.Categories().List(ctx, a)
	for _, change := range []func(*domain.ClassificationResult){func(r *domain.ClassificationResult) { r.Confidence = math.NaN() }, func(r *domain.ClassificationResult) { r.Confidence = 1.1 }, func(r *domain.ClassificationResult) { r.Reasons = nil }, func(r *domain.ClassificationResult) { r.Reasons = []string{" "} }, func(r *domain.ClassificationResult) { r.ClassifierName = "" }, func(r *domain.ClassificationResult) { r.Timestamp = time.Time{} }} {
		run := assessment(t, s, a, email, categories[0].ID)
		change(&run.Results[0])
		if err := s.Classifications().Replace(ctx, run); !errors.Is(err, repository.ErrInvalid) {
			t.Fatal("invalid result persisted", err)
		}
	}
	if count(t, s, "classification_results") != 0 || count(t, s, "classification_runs") != 0 {
		t.Fatal("invalid result wrote assessment")
	}
}

func TestClassificationAndAbstentionSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := account(t, s, "a")
	id := saveEmail(t, s, sampleEmail(a, "classified"))
	other := saveEmail(t, s, sampleEmail(a, "unclassified"))
	categories, _ := s.Categories().List(ctx, a)
	run := assessment(t, s, a, id, categories[0].ID)
	empty := assessment(t, s, a, other, "")
	if err = s.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = s.Classifications().Replace(ctx, empty); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.Classifications().Get(ctx, a, id)
	if err != nil || !reflect.DeepEqual(saved, run) {
		t.Fatal("prediction lost on restart", err)
	}
	saved, err = s.Classifications().Get(ctx, a, other)
	if err != nil || !reflect.DeepEqual(saved, empty) {
		t.Fatal("abstention lost on restart", err)
	}
}
func TestUpgradeVersionFourPreservesLegacyClassificationEvidence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for i, entry := range entries[:4] {
		body, _ := migrations.ReadFile("migrations/" + entry.Name())
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", i+1, fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
			t.Fatal(err)
		}
	}
	old := &Store{db: db, version: 4}
	a := account(t, old, "a")
	id := saveEmail(t, old, sampleEmail(a, "mail"))
	categories, _ := old.Categories().List(ctx, a)
	if _, err = db.Exec("INSERT INTO classification_results VALUES(?,?,?,.5,'old-version','old-hash','Legacy synthetic explanation',1)", a, id, categories[0].ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var name, reasons string
	if err = s.db.QueryRow("SELECT classifier_name,reasons_json FROM classification_results WHERE account_id=? AND email_id=?", a, id).Scan(&name, &reasons); err != nil || name != "legacy" || reasons != `["Legacy synthetic explanation"]` {
		t.Fatal("legacy evidence lost", err)
	}
	if _, err = s.Classifications().Get(ctx, a, id); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("legacy rows presented as current")
	}
	if err = s.Categories().Delete(ctx, a, categories[0].ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("legacy reference protection lost")
	}
}
