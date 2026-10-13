package app

import (
	"context"
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
	if r.Body != email.BodyText || r.Message.BodyFormat != "plain_text" || r.Message.CategoryState != "not_reviewed" {
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
