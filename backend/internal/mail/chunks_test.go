package mail

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/mail/wordpiece"
	"mailpilot.local/core/internal/repository"
)

func chunkMail(body string) repository.StoredEmail {
	e := domain.Email{ID: "email", AccountID: "account", ProviderID: "provider", Subject: "Project update", BodyText: body, BodyState: "complete", ReceivedAt: time.Unix(1700000000, 0)}
	hash, _ := repository.EmailContentHash(e)
	return repository.StoredEmail{Email: e, ContentHash: hash}
}
func buildChunks(t *testing.T, stored repository.StoredEmail) repository.ChunkSnapshot {
	t.Helper()
	c, err := NewChunker()
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Build(context.Background(), stored)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Sections(context.Background(), stored.Email)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := wordpiece.New()
	if err != nil {
		t.Fatal(err)
	}
	for i, chunk := range got.Chunks {
		source := p.Sources[chunk.SourceKind]
		if chunk.Index != i || chunk.Text != source[chunk.StartOffset:chunk.EndOffset] || !utf8.ValidString(chunk.Text) || chunk.SectionStartOffset > chunk.StartOffset || chunk.SectionEndOffset < chunk.EndOffset {
			t.Fatal("invalid source mapping")
		}
		tokens, err := tok.Tokens(context.Background(), ModelInput(chunk))
		if err != nil || len(tokens)+2 != chunk.TokenCount || chunk.TokenCount > 256 {
			t.Fatal("incorrect model-input budget", err)
		}
	}
	return got
}
func bodyChunks(chunks []repository.ChunkInput) []repository.ChunkInput {
	body := []repository.ChunkInput{}
	for _, c := range chunks {
		if c.SectionType != "subject" {
			body = append(body, c)
		}
	}
	return body
}
func TestShortChunkPreservesSourceAndDeterminism(t *testing.T) {
	stored := chunkMail("Hello team.\r\n\r\nPlease review the update.")
	before := stored
	got := buildChunks(t, stored)
	again := buildChunks(t, stored)
	if !reflect.DeepEqual(before, stored) || !reflect.DeepEqual(got, again) {
		t.Fatal("nondeterministic or mutated input")
	}
	body := bodyChunks(got.Chunks)
	if len(body) != 1 || body[0].Text != "Hello team.\n\nPlease review the update." || body[0].SectionType != "authored" || got.ContentStatus != "complete" {
		t.Fatal("short source not preserved")
	}
	if !strings.Contains(body[0].ContextText, "Subject: Project update") || !strings.Contains(body[0].ContextText, "Section: authored") {
		t.Fatal("missing bounded context")
	}
}
func TestLongChunksOverlapAndPreferParagraphs(t *testing.T) {
	paragraph := strings.Repeat("review the project update now. ", 15)
	stored := chunkMail(strings.Repeat(paragraph+"\n\n", 30))
	got := buildChunks(t, stored)
	body := bodyChunks(got.Chunks)
	if len(body) < 10 {
		t.Fatal("long mail not chunked")
	}
	tokenizer, _ := wordpiece.New()
	tokens, err := tokenizer.Tokens(context.Background(), strings.ReplaceAll(stored.Email.BodyText, "\r\n", "\n"))
	if err != nil {
		t.Fatal(err)
	}
	boundaries := 0
	for i, c := range body {
		if strings.HasSuffix(c.Text, "\n\n") || strings.HasSuffix(strings.TrimSpace(c.Text), ".") {
			boundaries++
		}
		if i == 0 {
			continue
		}
		previous := body[i-1]
		if c.StartOffset <= previous.StartOffset || c.StartOffset > previous.EndOffset {
			t.Fatal("overlap did not advance")
		}
		overlap := 0
		for _, tok := range tokens {
			if tok.Start >= c.StartOffset && tok.End <= previous.EndOffset {
				overlap++
			}
		}
		if overlap < 32 {
			t.Fatalf("overlap=%d; want at least 32", overlap)
		}
	}
	if boundaries < len(body)-1 {
		t.Fatal("sentence/paragraph boundaries ignored")
	}
	if body[0].StartOffset != 0 || body[len(body)-1].EndOffset != len(stored.Email.BodyText) {
		t.Fatal("source coverage lost")
	}
}
func TestQuotedForwardedAndSignatureSections(t *testing.T) {
	body := "My reply.\n\n> An inline quote.\nMy answer after the quote.\n-- \nAugusta\nOn Monday, Pat wrote:\nOlder reply.\n---------- Forwarded message ---------\nFrom: sender@example.test\nForwarded-only evidence.\n-----Original Message-----\nStill older forwarded evidence."
	got := buildChunks(t, chunkMail(body))
	kinds := map[string]string{}
	for _, c := range bodyChunks(got.Chunks) {
		kinds[c.SectionType] += c.Text
	}
	for kind, text := range map[string]string{"authored": "My answer after the quote.", "quoted": "Older reply.", "signature": "Augusta", "forwarded": "Still older forwarded evidence."} {
		if !strings.Contains(kinds[kind], text) {
			t.Fatalf("%s content lost or mislabeled", kind)
		}
	}
	if !strings.Contains(kinds["forwarded"], "Forwarded-only evidence.") {
		t.Fatal("forwarded evidence discarded")
	}
}
func TestHTMLAndMultipartAlternativesPreserveUniqueEvidence(t *testing.T) {
	html := `<div>Hello team.</div><blockquote>Quoted history.</blockquote><div class="gmail_signature">Signature person</div><script>never-index-this</script><img src="https://tracker.invalid/pixel">`
	stored := chunkMail("")
	stored.Email.BodyHTML = &html
	got := buildChunks(t, stored)
	kinds := map[string]string{}
	for _, c := range bodyChunks(got.Chunks) {
		kinds[c.SectionType] += c.Text
		if strings.Contains(c.Text, "never-index-this") || strings.Contains(c.Text, "tracker.invalid") {
			t.Fatal("non-text HTML indexed")
		}
	}
	if !strings.Contains(kinds["authored"], "Hello team.") || !strings.Contains(kinds["quoted"], "Quoted history.") || !strings.Contains(kinds["signature"], "Signature person") {
		t.Fatal("HTML structural provenance lost")
	}
	// Cached HTML-only messages also have a flattened BodyText from Gmail.
	flattened, err := HTMLText(html)
	if err != nil {
		t.Fatal(err)
	}
	stored.Email.BodyText = flattened
	flattenedResult := buildChunks(t, stored)
	all := ""
	for _, c := range bodyChunks(flattenedResult.Chunks) {
		all += c.Text
	}
	if strings.Count(all, "Hello team.") != 1 {
		t.Fatal("multipart alternatives duplicated")
	}
	html = `<p>Hello team.</p><div>---------- Forwarded message ---------</div><p>Unique forwarded evidence.</p>`
	stored.Email.BodyText = "Hello team."
	stored.Email.BodyHTML = &html
	extra := buildChunks(t, stored)
	all = ""
	forward := ""
	for _, c := range bodyChunks(extra.Chunks) {
		all += c.Text
		if c.SectionType == "forwarded" {
			forward += c.Text
		}
	}
	if strings.Count(all, "Hello team.") != 1 || !strings.Contains(forward, "Unique forwarded evidence.") {
		t.Fatal("additional HTML forward lost or alternative duplicated")
	}
}
func TestUnicodeMissingAndPartialBodies(t *testing.T) {
	unicode := chunkMail(strings.Repeat("Café e\u0301 東京 한글 👩‍💻. ", 100))
	got := buildChunks(t, unicode)
	if len(bodyChunks(got.Chunks)) < 2 {
		t.Fatal("Unicode not chunked")
	}
	for _, state := range []string{"", "unavailable", "complete"} {
		stored := chunkMail("")
		stored.Email.BodyState = state
		missing := buildChunks(t, stored)
		if missing.ContentStatus != "unavailable" || len(bodyChunks(missing.Chunks)) != 0 {
			t.Fatal("missing body claimed complete")
		}
		stored.Email.BodyText = "An available fragment."
		partial := buildChunks(t, stored)
		want := "partial"
		if state == "complete" {
			want = "complete"
		}
		if partial.ContentStatus != want {
			t.Fatal("incorrect body coverage")
		}
	}
}
func TestHugeHeadersDoNotEnterRepeatedContext(t *testing.T) {
	stored := chunkMail(strings.Repeat("Body evidence. ", 1000))
	stored.Email.Subject = strings.Repeat("A long subject ", 1000)
	stored.Email.Headers = []domain.EmailHeader{{Name: "Received", Value: strings.Repeat("large-header", 100000)}}
	got := buildChunks(t, stored)
	for _, c := range got.Chunks {
		if len(c.ContextText) > 400 || strings.Contains(c.ContextText, "large-header") {
			t.Fatal("headers or huge subject repeated")
		}
	}
	subject := []repository.ChunkInput{}
	for _, c := range got.Chunks {
		if c.SectionType == "subject" {
			subject = append(subject, c)
		}
	}
	if len(subject) < 2 || subject[len(subject)-1].EndOffset != len(stored.Email.Subject) {
		t.Fatal("full subject source discarded")
	}
}
func TestMalformedBoundedAndCanceledChunking(t *testing.T) {
	c, err := NewChunker()
	if err != nil {
		t.Fatal(err)
	}
	cases := []repository.StoredEmail{chunkMail(strings.Repeat("x", MaxBodyBytes+1)), chunkMail(string([]byte{255})), chunkMail(strings.Repeat("a", 65537))}
	nested := chunkMail("")
	html := strings.Repeat("<div>", 140) + "body" + strings.Repeat("</div>", 140)
	nested.Email.BodyHTML = &html
	cases = append(cases, nested)
	for _, stored := range cases {
		if _, err = c.Build(context.Background(), stored); err == nil {
			t.Fatal("malformed/oversized input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Build(ctx, chunkMail("body")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}
