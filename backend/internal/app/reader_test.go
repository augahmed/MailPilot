package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func TestCachedReaderPagingIsolationAndRevision(t *testing.T) {
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	email.BodyText = strings.Repeat("<script>危険</script>\n\x01", 3000)
	email.CCRecipients = []domain.EmailAddress{{Address: "copy@example.test"}}
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader(review.session, store.Emails(), review, nil)
	request := ReadMailRequest{AccountID: "a", EmailID: string(email.ID)}
	var body strings.Builder
	for {
		response := reader.Read(ctx, request)
		if response.Code != "" || response.Message == nil {
			t.Fatalf("read failed: %s", response.Code)
		}
		encoded, _ := json.Marshal(response)
		if len(encoded) > readerResponseBytes || len(response.Message.CC) != 1 || response.Message.Phishing == nil {
			t.Fatal("missing metadata or oversized frame")
		}
		body.WriteString(response.Body)
		request.Offset, request.Revision = response.NextOffset, response.Revision
		if response.NextOffset == response.TotalBytes {
			break
		}
	}
	if body.String() != email.BodyText {
		t.Fatal("body changed during paging")
	}
	email.BodyText = "changed"
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	if r := reader.Read(ctx, request); r.Code != "stale" || r.Message != nil || r.Body != "" {
		t.Fatal("stale content disclosed")
	}
	request = ReadMailRequest{AccountID: "other", EmailID: string(email.ID)}
	if r := reader.Read(ctx, request); r.Code != "unauthorized" || r.Message != nil {
		t.Fatal("account check missing")
	}
	request = ReadMailRequest{AccountID: "a", EmailID: "missing"}
	if r := reader.Read(ctx, request); r.Code != "not_found" {
		t.Fatal("missing message handling")
	}
}
func TestCachedReaderHTMLFallbackAndSavedCategory(t *testing.T) {
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	html := `<script>alert(1)</script><style>bad</style><p>Hello reader</p><img src="https://tracking.example.test/pixel"><blockquote>Quoted history</blockquote>`
	email.BodyText = ""
	email.BodyHTML = &html
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader(review.session, store.Emails(), review, nil)
	request := ReadMailRequest{AccountID: "a", EmailID: string(email.ID)}
	r := reader.Read(ctx, request)
	if r.Code != "" || r.Message.BodyFormat != "html_text" || !strings.Contains(r.Body, "Quoted history") || strings.Contains(r.Body, "alert") || strings.Contains(r.Body, "tracking") || strings.Contains(r.Body, "<") {
		t.Fatal("unsafe or incomplete HTML fallback")
	}
	if r.Message.CategoryState != "not_reviewed" {
		t.Fatal("reader should not classify")
	}
	review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	r = reader.Read(ctx, request)
	if r.Message.CategoryState == "not_reviewed" {
		t.Fatal("saved classification not shown")
	}
	email.BodyText = "Original plain text\n> quoted history"
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	r = reader.Read(ctx, request)
	if !strings.Contains(r.Body, email.BodyText) || !strings.Contains(r.Body, "Quoted history") || r.Message.BodyFormat != "html_text" || r.Message.CategoryState != "not_reviewed" {
		t.Fatal("plain fallback or stale category incorrect")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	reader.session = func() (domain.AccountID, context.Context, error) { return "a", cancelled, nil }
	if r = reader.Read(ctx, request); r.Code != "unauthorized" || r.Body != "" {
		t.Fatal("revoked session disclosed content")
	}
}
func TestReadMailRequestValidation(t *testing.T) {
	for _, p := range []ReadMailRequest{{}, {AccountID: "a", EmailID: "e", Offset: 1}, {AccountID: "a", EmailID: "e", Revision: strings.Repeat("z", 64)}, {AccountID: "a", EmailID: "e", Offset: -1}} {
		if p.Valid() {
			t.Fatal("accepted invalid request")
		}
	}
}

func TestCachedReaderCleansTrackingArtifactsAndKeepsOriginalRiskEvidence(t *testing.T) {
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	rawURL := "<https://click.example.test/u/?qs=" + strings.Repeat("SyntheticTracking", 12) + ">"
	email.BodyText = "Special offers October 5–16.\n[image: Example logo]\n" + rawURL + "\n[image: Save 14% at Northshop]\n[image: Save 14% at Northshop]\n" + rawURL + "\n> Quoted appointment at noon.\n-- \nSam"
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	before, err := store.Emails().Get(ctx, "a", email.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader(review.session, store.Emails(), review, nil)
	r := reader.Read(ctx, ReadMailRequest{AccountID: "a", EmailID: string(email.ID)})
	if r.Code != "" || r.Message.BodyError || !strings.Contains(r.Body, "Save 14% at Northshop") || !strings.Contains(r.Body, "> Quoted appointment at noon.") || !strings.Contains(r.Body, "-- \nSam") {
		t.Fatal("clean reader lost meaningful body", r.Code)
	}
	if strings.Contains(r.Body, "[image:") || strings.Contains(r.Body, "SyntheticTracking") || strings.Count(r.Body, "Save 14% at Northshop") != 1 {
		t.Fatal("reader still displays transport clutter")
	}
	after, err := store.Emails().Get(ctx, "a", email.ID)
	if err != nil || after.ContentHash != before.ContentHash || after.Email.BodyText != email.BodyText || r.Message.Phishing == nil {
		t.Fatal("clean reader changed source/security evidence")
	}
	// A source-only cursor cannot page a differently normalized reader projection.
	stale := reader.Read(ctx, ReadMailRequest{AccountID: "a", EmailID: string(email.ID), Revision: before.ContentHash})
	if stale.Code != "stale" || stale.Body != "" {
		t.Fatal("reader revision omitted cleanup identity")
	}
}

func TestCachedReaderDoesNotAppendFormattedPlainAlternative(t *testing.T) {
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	email.BodyText = "*Build your professional\nnetwork.*\n\n*Campus Connect\nhttps://click.engage.example.test/*\nis free for students and alumni.\n\n*Need help?* Email support@example.test\n<support@example.test?utm_source=synthetic?subject=>\nfor support.\n\nUnique plain-only booking ABC123."
	html := `<p><strong>Build your professional network.</strong></p><p><a href="https://click.engage.example.test/">Campus Connect</a> is free for students and alumni.</p><p><b>Need help?</b> Email <a href="mailto:support@example.test">support@example.test</a> for support.</p>`
	email.BodyHTML = &html
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader(review.session, store.Emails(), review, nil)
	r := reader.Read(ctx, ReadMailRequest{AccountID: "a", EmailID: string(email.ID)})
	if r.Code != "" || r.Message == nil || r.Message.BodyError {
		t.Fatal("reader failed", r.Code)
	}
	for _, fact := range []string{"Build your professional network.", "Campus Connect", "Need help?", "support@example.test", "ABC123"} {
		if strings.Count(r.Body, fact) != 1 {
			t.Fatalf("reader duplicated or lost %q", fact)
		}
	}
	if strings.Contains(r.Body, "*") || strings.Contains(r.Body, "utm_") || strings.Contains(r.Body, "click.engage") {
		t.Fatal("plain-alternative formatting and link annotations leaked")
	}
	stored, err := store.Emails().Get(ctx, "a", email.ID)
	if err != nil || stored.Email.BodyText != email.BodyText || *stored.Email.BodyHTML != html {
		t.Fatal("reading altered original alternatives", err)
	}
}

func TestCachedReaderDeduplicatesBracketedNewsletterAndRevokesV3Cursor(t *testing.T) {
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	redirect := "https://email.news.example.test/e/c/" + strings.Repeat("SyntheticOpaque_", 16) + "/" + strings.Repeat("a", 64)
	html := `<h1>October project update</h1><p>Query projects using natural language. <a href="` + redirect + `">Blog</a></p><p>Usage-based logs include monthly limits. <a href="` + redirect + `">Changelog</a></p><p>©2026 Example Inc.</p>`
	email.BodyHTML = &html
	email.BodyText = "October project update\n\n- Query projects using natural language. [Blog\n" + redirect + "\n]\n- Usage-based logs include monthly limits. [Changelog\n" + redirect + "\n]\n\n©2026 Example Inc.\n" + strings.Repeat("\u2007\u034f \u00ad ", 60)
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	before, err := store.Emails().Get(ctx, "a", email.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewCachedReader(review.session, store.Emails(), review, nil)
	request := ReadMailRequest{AccountID: "a", EmailID: string(email.ID)}
	got := reader.Read(ctx, request)
	if got.Code != "" || got.Message == nil || got.Message.BodyError {
		t.Fatal("reader failed", got.Code)
	}
	for _, fact := range []string{"October project update", "Query projects", "Usage-based logs", "©2026 Example"} {
		if strings.Count(got.Body, fact) != 1 {
			t.Fatalf("reader duplicates %q", fact)
		}
	}
	if strings.Contains(got.Body, "SyntheticOpaque") || strings.Contains(got.Body, "\u034f") {
		t.Fatal("reader retained padding or tracking payload")
	}
	old := sha256.Sum256([]byte("readable-v3\n" + before.ContentHash))
	request.Revision = hex.EncodeToString(old[:])
	if stale := reader.Read(ctx, request); stale.Code != "stale" || stale.Body != "" {
		t.Fatal("v3 cursor reused for v4 projection", stale.Code)
	}
	after, err := store.Emails().Get(ctx, "a", email.ID)
	if err != nil || after.ContentHash != before.ContentHash || after.Email.BodyText != email.BodyText || *after.Email.BodyHTML != html {
		t.Fatal("raw links/source were changed", err)
	}
}
