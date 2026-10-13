package embedding

import (
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/mail"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRetrievalCleanupPreservesInlineAnswersAndTrimsReliableSignatures(t *testing.T) {
	e := domain.Email{BodyState: "complete", Subject: "Re: FW: Updated details", BodyText: "On Monday, Pat wrote:\r\n> Quoted old answer\r\nUse the west door.\r\nThanks for checking.\r\n-- \r\nAdvertising signature"}
	doc, err := PrepareRetrieval(e)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := RetrievalBody(e)
	if strings.Contains(body, "Quoted") || strings.Contains(body, "signature") || !strings.Contains(body, "west door") || !strings.Contains(body, "Thanks") {
		t.Fatal("incorrect cleanup")
	}
	if doc.Passages[0].Text != "Updated details" {
		t.Fatal("subject normalization")
	}
	e.BodyText = "Current reply\nFrom: Person\nSent: Monday\nTo: Other\nSubject: Old conversation\nOld body"
	body, _ = RetrievalBody(e)
	if body != "Current reply" {
		t.Fatal("Outlook quote chain retained")
	}
	e.BodyText = "From: the north entrance, walk west.\nFrom: the west entrance, walk north.\nFrom: the south entrance, walk east.\nThanks\nMore substantive content"
	body, _ = RetrievalBody(e)
	if !strings.Contains(body, "substantive") {
		t.Fatal("ordinary prose trimmed")
	}
}
func TestPassagesCoverLongMultilingualTextAndAreDeterministic(t *testing.T) {
	e := domain.Email{BodyState: "complete", Subject: "A subject", BodyText: strings.Repeat("Long report with café and 東京. ", 1500) + "TAIL NEEDLE"}
	a, err := PrepareRetrieval(e)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PrepareRetrieval(e)
	if a.Hash != b.Hash || len(a.Passages) < 100 {
		t.Fatal("unstable or truncated document")
	}
	for i, p := range a.Passages {
		if p != b.Passages[i] || !utf8.ValidString(p.Text) || len(p.Text) > PassageBytes || p.Ordinal != i {
			t.Fatal("invalid deterministic passage")
		}
	}
	if !strings.Contains(a.Passages[len(a.Passages)-1].Text, "TAIL NEEDLE") {
		t.Fatal("tail was truncated")
	}
	e.BodyText += "\n> new quoted text"
	c, _ := PrepareRetrieval(e)
	if c.Hash != a.Hash {
		t.Fatal("quoted content invalidated cache")
	}
}
func TestRetrievalHTMLAndBounds(t *testing.T) {
	html := `<p>Useful new content</p><blockquote>Old chain</blockquote><div class="gmail_signature">Signature</div><script>active()</script>`
	doc, err := PrepareRetrieval(domain.Email{BodyState: "complete", BodyHTML: &html})
	if err != nil || len(doc.Passages) != 1 || doc.Passages[0].Text != "Useful new content" {
		t.Fatal("HTML cleanup", err)
	}
	derived, _ := mail.HTMLText(html)
	normalized, err := PrepareRetrieval(domain.Email{BodyState: "complete", BodyText: derived, BodyHTML: &html})
	if err != nil || normalized.Hash != doc.Hash {
		t.Fatal("normalized HTML lost structural cleanup")
	}
	if _, err := PrepareRetrieval(domain.Email{BodyState: "unavailable"}); err != ErrUnavailable {
		t.Fatal("unavailable body accepted")
	}
	if _, err := PrepareRetrieval(domain.Email{BodyState: "complete", BodyText: strings.Repeat("x", 1048577)}); err != ErrInput {
		t.Fatal("unbounded body accepted")
	}
}
