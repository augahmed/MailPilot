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
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func account(t *testing.T, s *Store, id string) domain.AccountID {
	t.Helper()
	result, err := s.Accounts().Upsert(context.Background(), domain.Account{ID: domain.AccountID(id), Provider: "fixture", ProviderAccountID: "remote-" + id, Address: domain.EmailAddress{Name: "Example", Address: id + "@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func sampleEmail(a domain.AccountID, id string) domain.Email {
	size := int64(0)
	html := "<p>Local only</p>"
	return domain.Email{
		AccountID: a, ProviderID: domain.ProviderMessageID(id), ProviderThreadID: "thread/opaque:9007199254740993",
		Sender:       domain.EmailAddress{Name: "Sender", Address: "sender@example.test"},
		Recipients:   []domain.EmailAddress{{Name: "First", Address: "first@example.test"}, {Name: "Second", Address: "second@example.test"}},
		CCRecipients: []domain.EmailAddress{{Name: "CC", Address: "cc@example.test"}}, Subject: "Subject", BodyText: "Cached plain text", BodyHTML: &html,
		ReceivedAt:  time.Date(2026, 10, 10, 9, 30, 0, 123456789, time.FixedZone("offset", -4*3600)),
		Headers:     []domain.EmailHeader{{Name: "Received", Value: "first"}, {Name: "received", Value: "second"}},
		Attachments: []domain.AttachmentMetadata{{ProviderID: "attachment", PartID: "1.2", Filename: "../untrusted.txt", MIMEType: "text/plain", SizeBytes: &size}, {PartID: "1.3", Filename: "unknown", MIMEType: "application/octet-stream"}},
		Labels:      []domain.EmailLabel{{AccountID: a, ProviderID: "work", Name: "Work", Kind: domain.LabelKindUser}, {AccountID: a, ProviderID: "INBOX", Name: "Inbox", Kind: domain.LabelKindSystem}},
	}
}
func saveEmail(t *testing.T, s *Store, e domain.Email) domain.EmailID {
	t.Helper()
	id, err := s.Emails().Upsert(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCleanDatabaseRunsAllMigrations(t *testing.T) {
	s := testStore(t)
	for _, table := range []string{"accounts", "emails", "email_addresses", "labels", "email_labels", "categories", "email_categories", "classification_results", "phishing_results", "mailbox_actions", "mailbox_action_items", "email_chunks", "chunk_embeddings", "index_state"} {
		if count(t, s, table) != 0 {
			t.Fatalf("new table %s not empty", table)
		}
	}
	if count(t, s, "schema_migrations") != 19 {
		t.Fatal("missing migration")
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violations")
	}
	// Credentials can only be referenced; no token/password columns are introduced.
	columns, err := s.db.Query("SELECT name FROM pragma_table_info('accounts')")
	if err != nil {
		t.Fatal(err)
	}
	defer columns.Close()
	for columns.Next() {
		var name string
		if err := columns.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "token" || name == "access_token" || name == "refresh_token" || name == "password" {
			t.Fatal("credential column present")
		}
	}
}

func TestUpgradeVersionOnePreservesSettings(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrations.ReadFile("migrations/001_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL);` + string(body)); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(body))
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES(1,?,0)", checksum); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO settings VALUES('theme','"dark"',0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion() != 19 || count(t, s, "settings") != 1 {
		t.Fatal("upgrade lost data")
	}
	_ = s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count(t, s, "schema_migrations") != 19 {
		t.Fatal("migrations repeated")
	}
}

func TestMigrationTwoFailureIsAtomic(t *testing.T) {
	// Build a version-1 database with a conflicting name late in migration 2.
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := migrations.ReadFile("migrations/001_settings.sql")
	_, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL);` + string(body) + `CREATE TABLE categories(existing TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES(1,?,0)", fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if s, err := Open(context.Background(), dir); err == nil {
		_ = s.Close()
		t.Fatal("conflicting migration accepted")
	}
	db, err = sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='emails'").Scan(&n); err != nil || n != 0 {
		t.Fatal("partial migration committed", err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n != 1 {
		t.Fatal("failed migration recorded", err)
	}
}

func TestAccountUpsertPreservesIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	id, err := s.Accounts().Upsert(ctx, domain.Account{Provider: "fixture", ProviderAccountID: "remote-a", Address: domain.EmailAddress{Address: "changed@example.test"}, CredentialRef: "os-keyring/account-a"})
	if err != nil || id != a || count(t, s, "accounts") != 1 {
		t.Fatalf("non-idempotent account: %v", err)
	}
	got, err := s.Accounts().Get(ctx, a)
	if err != nil || got.Address.Address != "changed@example.test" || got.CredentialRef != "os-keyring/account-a" {
		t.Fatalf("account mismatch: %+v %v", got, err)
	}
	if _, err := s.Accounts().Upsert(ctx, domain.Account{ID: "wrong", Provider: "fixture", ProviderAccountID: "remote-a"}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("account ID rebound", err)
	}
}

func TestEmailRoundTripAndIdempotentReplacement(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	e := sampleEmail(a, "message")
	id := saveEmail(t, s, e)
	stored, err := s.Emails().Get(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	got := stored.Email
	if got.BodyHTML == nil || *got.BodyHTML != *e.BodyHTML || got.BodyText != e.BodyText || got.Subject != e.Subject || got.ProviderThreadID != e.ProviderThreadID {
		t.Fatal("content lost")
	}
	if !reflect.DeepEqual(got.Sender, e.Sender) || !reflect.DeepEqual(got.Recipients, e.Recipients) || !reflect.DeepEqual(got.CCRecipients, e.CCRecipients) || !reflect.DeepEqual(got.Headers, e.Headers) || !reflect.DeepEqual(got.Attachments, e.Attachments) {
		t.Fatalf("normalized data lost: %+v", got)
	}
	if !got.ReceivedAt.Equal(instant(e.ReceivedAt.UnixMilli())) || got.ReceivedAt.Location() != time.UTC {
		t.Fatal("timestamp not normalized")
	}
	if got.ID == "" || stored.ContentHash == "" || stored.CreatedAt.IsZero() {
		t.Fatal("missing persistence metadata")
	}
	if replay := saveEmail(t, s, e); replay != id {
		t.Fatal("replay changed identity")
	}
	if count(t, s, "emails") != 1 || count(t, s, "email_addresses") != 4 || count(t, s, "email_labels") != 2 || count(t, s, "labels") != 2 {
		t.Fatal("duplicate child data")
	}
	e.Subject = "Changed"
	e.Recipients = e.Recipients[:1]
	e.CCRecipients = nil
	e.Labels = e.Labels[:1]
	e.BodyHTML = nil
	if saveEmail(t, s, e) != id {
		t.Fatal("update changed identity")
	}
	updated, err := s.Emails().GetByProviderID(ctx, a, e.ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Email.Subject != "Changed" || updated.Email.BodyHTML != nil || len(updated.Email.CCRecipients) != 0 || len(updated.Email.Labels) != 1 || len(updated.Email.Recipients) != 1 {
		t.Fatal("snapshot not replaced")
	}
	if !updated.CreatedAt.Equal(stored.CreatedAt) || updated.ContentHash == stored.ContentHash {
		t.Fatal("metadata incorrect")
	}
	empty := ""
	e.BodyHTML = &empty
	saveEmail(t, s, e)
	updated, err = s.Emails().Get(ctx, a, id)
	if err != nil || updated.Email.BodyHTML == nil || *updated.Email.BodyHTML != "" {
		t.Fatal("empty HTML collapsed to absent")
	}
}

func TestEmailAccountIsolationAndUniqueness(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	first := saveEmail(t, s, sampleEmail(a, "same-remote-id"))
	second := saveEmail(t, s, sampleEmail(b, "same-remote-id"))
	if first == second || count(t, s, "emails") != 2 {
		t.Fatal("provider IDs must be unique per account only")
	}
	if _, err := s.Emails().Get(ctx, b, first); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
	e := sampleEmail(a, "same-remote-id")
	e.ID = second
	if _, err := s.Emails().Upsert(ctx, e); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("ID rebound", err)
	}
	e = sampleEmail(a, "new-id")
	e.Labels[0].AccountID = b
	if _, err := s.Emails().Upsert(ctx, e); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("cross-account label accepted", err)
	}
	page, err := s.Emails().List(ctx, a, repository.Page{Limit: 100})
	if err != nil || len(page) != 1 || page[0].Email.ID != first {
		t.Fatal("list leaks accounts", err)
	}
}

func TestEmailBatchRollbackAndCancellation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	original := sampleEmail(a, "original")
	id := saveEmail(t, s, original)
	foreign := saveEmail(t, s, sampleEmail(b, "foreign"))
	changed := original
	changed.BodyText = "should roll back"
	changed.Recipients = nil
	collision := sampleEmail(a, "collision")
	collision.ID = foreign
	if ids, err := s.Emails().UpsertBatch(ctx, a, []domain.Email{changed, collision}); !errors.Is(err, repository.ErrConflict) || ids != nil {
		t.Fatal("batch unexpectedly succeeded", err)
	}
	got, err := s.Emails().Get(ctx, a, id)
	if err != nil || got.Email.BodyText != original.BodyText || len(got.Email.Recipients) != 2 {
		t.Fatal("batch partially committed", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Emails().Upsert(cancelled, sampleEmail(a, "cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not propagated", err)
	}
	if count(t, s, "emails") != 2 {
		t.Fatal("failed batch left rows")
	}
}

func TestEmailPaginationStableOrdering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	for _, id := range []domain.EmailID{"one", "two", "three"} {
		e := sampleEmail(a, string(id))
		e.ID = id
		saveEmail(t, s, e)
	}
	first, err := s.Emails().List(ctx, a, repository.Page{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Emails().List(ctx, a, repository.Page{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 1 || first[0].Email.ID != "two" || second[0].Email.ID != "one" {
		t.Fatal("unstable tied timestamp ordering")
	}
	if _, err := s.Emails().List(ctx, a, repository.Page{Limit: 101}); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("unbounded page accepted")
	}
}

func TestConcurrentEmailReplaysDoNotDuplicate(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "a")
	e := sampleEmail(a, "same")
	var wg sync.WaitGroup
	ids := make(chan domain.EmailID, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, err := s.Emails().Upsert(context.Background(), e); ids <- id; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected domain.EmailID
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatal("race changed identity")
		}
	}
	if count(t, s, "emails") != 1 || count(t, s, "email_addresses") != 4 {
		t.Fatal("concurrent replay duplicated data")
	}
}

func TestCategoryAssignmentsAreLocalAndAccountScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	e := sampleEmail(a, "mail")
	id := saveEmail(t, s, e)
	category, err := s.Categories().Save(ctx, a, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, Name: "Follow up"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Categories().Save(ctx, a, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, Name: "Follow up"})
	if !errors.Is(err, repository.ErrConflict) || replay != "" {
		t.Fatal("category duplicated", err)
	}
	foreign, err := s.Categories().Save(ctx, b, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, Name: "Follow up"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Categories().Assign(ctx, a, id, category); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Categories().Assign(ctx, a, id, foreign); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign category assigned", err)
	}
	if err := s.Categories().Assign(ctx, b, id, category); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign association accepted", err)
	}
	saveEmail(t, s, e)
	got, err := s.Emails().Get(ctx, a, id)
	if err != nil || len(got.Email.Categories) != 1 || got.Email.Categories[0].ID != category {
		t.Fatal("sync erased local category", err)
	}
	if _, err := s.Categories().Save(ctx, b, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, ID: category, Name: "takeover"}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("category reassigned", err)
	}
	if _, err := s.Categories().Get(ctx, b, category); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign category read", err)
	}
	if _, err := s.Categories().Save(ctx, a, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, ID: category, Name: "Renamed"}); err != nil {
		t.Fatal(err)
	}
	listed, err := s.Categories().List(ctx, a)
	if err != nil || len(listed) != 7 || listed[5].Name != "Renamed" {
		t.Fatal("category listing incorrect", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Categories().Unassign(ctx, a, id, category); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, s, "email_categories") != 0 {
		t.Fatal("unassign failed")
	}
}

func TestAnalysisResultsAndContentInvalidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	e := sampleEmail(a, "mail")
	id := saveEmail(t, s, e)
	stored, err := s.Emails().Get(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	category, err := s.Categories().Save(ctx, a, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	assessed := time.Now().UTC().Truncate(time.Millisecond)
	classification := assessment(t, s, a, id, category).Results[0]
	classification.Timestamp = assessed
	phishing := domain.PhishingResult{AccountID: a, EmailID: id, Score: 65, AnalyzerVersion: "rules/1", InputHash: stored.ContentHash, Findings: []domain.PhishingFinding{{Code: "link_mismatch", Explanation: "Different link target", Evidence: "example.test"}}, AssessedAt: assessed}
	for i := 0; i < 2; i++ {
		if err := s.Classifications().Replace(ctx, runWithResult(classification)); err != nil {
			t.Fatal(err)
		}
		if err := s.Emails().SavePhishingResult(ctx, phishing); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, s, "classification_results") != 1 || count(t, s, "phishing_results") != 1 {
		t.Fatal("analysis replay duplicated rows")
	}
	got, err := s.Emails().GetPhishingResult(ctx, a, id)
	if err != nil || !reflect.DeepEqual(got, phishing) {
		t.Fatal("phishing result lost", err)
	}
	run, err := s.Classifications().Get(ctx, a, id)
	classes := run.Results
	if err != nil || len(classes) != 1 || !reflect.DeepEqual(classes[0], classification) {
		t.Fatal("classification result lost", err)
	}
	if _, err := s.Emails().GetPhishingResult(ctx, b, id); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign result returned", err)
	}
	if _, err := s.Classifications().Get(ctx, b, id); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign classifications returned", err)
	}
	classification.Confidence = math.NaN()
	if err := s.Classifications().Replace(ctx, runWithResult(classification)); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("NaN confidence accepted")
	}
	classification.Confidence = .8
	phishing.Score = 101
	if err := s.Emails().SavePhishingResult(ctx, phishing); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("invalid risk accepted")
	}
	phishing.Score = 65
	phishing.AssessedAt = assessed.Add(-time.Second)
	if err := s.Emails().SavePhishingResult(ctx, phishing); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("older result overwrote newer")
	}
	phishing.AssessedAt = assessed
	e.Labels = nil
	saveEmail(t, s, e)
	if count(t, s, "phishing_results") != 1 {
		t.Fatal("label-only update invalidated content analysis")
	}
	e.BodyText = "changed evidence"
	saveEmail(t, s, e)
	if count(t, s, "classification_results") != 0 || count(t, s, "phishing_results") != 0 {
		t.Fatal("stale analysis survived changed content")
	}
	if err := s.Emails().SavePhishingResult(ctx, phishing); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale analyzer result accepted")
	}
	if err := s.Classifications().Replace(ctx, runWithResult(classification)); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale classifier result accepted")
	}
}

func TestDatabaseConstraintsPreventCrossAccountLinks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	id := saveEmail(t, s, sampleEmail(a, "mail"))
	saveEmail(t, s, sampleEmail(b, "mail"))
	var foreignLabel domain.LabelID
	if err := s.db.QueryRow("SELECT id FROM labels WHERE account_id=? LIMIT 1", b).Scan(&foreignLabel); err != nil {
		t.Fatal(err)
	}
	category, err := s.Categories().Save(ctx, b, domain.EmailCategory{Description: "Synthetic category description", Enabled: true, Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO email_labels VALUES(?,?,?)", []any{a, id, foreignLabel}},
		{"INSERT INTO email_categories VALUES(?,?,?)", []any{a, id, category}},
		{"INSERT INTO classification_results(account_id,email_id,category_id,confidence,classifier_version,input_hash,explanation,assessed_at) VALUES(?,?,?,.5,'v1','hash','explanation',0)", []any{a, id, category}},
		{"INSERT INTO phishing_results VALUES(?,?,50,'v1','hash','[]',0)", []any{b, id}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err == nil {
			t.Fatal("foreign key constraint missing")
		}
	}
}

func TestUnencodableEmailRollsBackBatch(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "encoding")
	good := sampleEmail(a, "good")
	invalid := sampleEmail(a, "invalid")
	invalid.ReceivedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Emails().UpsertBatch(context.Background(), a, []domain.Email{good, invalid}); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("unencodable content was hashed and accepted")
	}
	if count(t, s, "emails") != 0 || count(t, s, "email_addresses") != 0 {
		t.Fatal("failed hash left partial batch data")
	}
}

func TestVersionTwoUpgradePreservesExistingMail(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"001_settings.sql", "002_mail_storage.sql"} {

		body, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(body)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", i+1, fmt.Sprintf("%x", sha256.Sum256(body))); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,provider,provider_account_id,address,display_name,credential_ref,created_at,updated_at) VALUES('a','fixture','a','a@example.test','','',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO emails(id,account_id,provider_message_id,provider_thread_id,subject,body_text,received_at,headers_json,attachments_json,content_hash,created_at,updated_at) VALUES('mail','a','remote','thread','Subject','Preserved',1,'[]','[]','hash',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Emails().GetByProviderID(context.Background(), "a", "remote")
	if err != nil || got.Email.BodyText != "Preserved" || got.Email.BodyState != "" || got.Email.ContentRevision != "" {
		t.Fatal("upgrade corrupted legacy mail", err)
	}
}
