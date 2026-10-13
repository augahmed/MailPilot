package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync/atomic"
	"testing"
)

func testDraft() provider.DraftInput {
	return provider.DraftInput{Recipients: []domain.EmailAddress{{Address: "host@example.test"}}, Subject: "Re: Synthetic meeting", BodyText: "Human reviewed reply.\nThanks.", ThreadID: "thread", MessageID: "<mailpilot-test@mailpilot.invalid>", InReplyTo: "<parent@example.test>", References: "<parent@example.test>"}
}
func TestDraftSubjectPreservesReviewedUnicodeAndEncodedWords(t *testing.T) {
	for _, subject := range []string{"Re: Synthetic meeting", "Re: =?utf-8?B?YXR0YWNr?=", strings.Repeat("計画 📨 ", 20)} {
		d := testDraft()
		d.Subject = subject
		m, err := mail.ReadMessage(strings.NewReader(string(draftMIME(d, "owner@example.test"))))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
		if err != nil || decoded != subject {
			t.Fatal("reviewed subject changed", err)
		}
		if len(m.Header["Subject"]) != 1 {
			t.Fatal("injected subject header")
		}
	}
}
func TestGmailDraftCreationMIMEAndReadOnlyReconciliation(t *testing.T) {
	d := testDraft()
	var posts, reads atomic.Int32
	var fail, edited, missing, duplicate atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("missing credentials")
		}
		if r.URL.Path == "/profile" && r.Method == "GET" {
			_, _ = io.WriteString(w, `{"emailAddress":"owner@example.test"}`)
			return
		}
		if r.URL.Path == "/drafts" && r.Method == "POST" {
			posts.Add(1)
			var v draftWire
			if json.NewDecoder(r.Body).Decode(&v) != nil {
				t.Error("invalid request")
			}
			raw, e := base64.RawURLEncoding.DecodeString(v.Message.Raw)
			if e != nil {
				t.Error("not base64url")
			}
			m, e := mail.ReadMessage(strings.NewReader(string(raw)))
			if e != nil {
				t.Error("invalid MIME")
				w.WriteHeader(500)
				return
			}
			body, _ := io.ReadAll(quotedprintable.NewReader(m.Body))
			if canonicalBody(string(body)) != d.BodyText || m.Header.Get("In-Reply-To") != d.InReplyTo || m.Header.Get("References") != d.References || m.Header.Get("Message-ID") != d.MessageID || v.Message.ThreadID != "thread" {
				t.Error("lost reply metadata or edited body")
			}
			from, _ := m.Header.AddressList("From")
			if len(from) != 1 || from[0].Address != "owner@example.test" {
				t.Error("forged sender")
			}
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
			_, _ = io.WriteString(w, `{"id":"draft","message":{"id":"message","threadId":"thread"}}`)
			return
		}
		if r.URL.Path == "/drafts" && r.Method == "GET" {
			reads.Add(1)
			if r.URL.Query().Get("q") != "rfc822msgid:"+d.MessageID {
				t.Error("unscoped reconciliation")
			}
			if missing.Load() {
				_, _ = io.WriteString(w, `{"drafts":[]}`)
			} else if duplicate.Load() {
				_, _ = io.WriteString(w, `{"drafts":[{"id":"draft"},{"id":"other"}]}`)
			} else {
				_, _ = io.WriteString(w, `{"drafts":[{"id":"draft","message":{"id":"message"}}]}`)
			}
			return
		}
		if r.URL.Path == "/drafts/draft" && r.Method == "GET" {
			copy := d
			if edited.Load() {
				copy.BodyText = "Changed elsewhere"
			}
			var v draftWire
			v.ID = "draft"
			v.Message.ID = "message"
			v.Message.ThreadID = "thread"
			v.Message.Raw = base64.RawURLEncoding.EncodeToString(draftMIME(copy, "owner@example.test"))
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		t.Error("unexpected sending/destructive endpoint or method")
		w.WriteHeader(405)
	}))
	defer server.Close()
	p := NewProvider("a", syntheticTokens{})
	p.baseURL = server.URL
	p.client = server.Client()
	ctx := context.Background()
	if !p.Capabilities().CreateDrafts {
		t.Fatal("missing draft capability")
	}
	got, e := p.CreateDraft(ctx, d)
	if e != nil || got.AccountID != "a" || got.ProviderDraftID != "draft" {
		t.Fatal("create failed", e)
	}
	got, e = p.FindDraft(ctx, d)
	if e != nil || got.ProviderDraftID != "draft" || posts.Load() != 1 {
		t.Fatal("reconcile wrote or mismatched", e)
	}
	edited.Store(true)
	if _, e = p.FindDraft(ctx, d); e != provider.ErrConflict {
		t.Fatal("changed draft accepted", e)
	}
	edited.Store(false)
	missing.Store(true)
	if _, e = p.FindDraft(ctx, d); e != provider.ErrNotFound {
		t.Fatal("missing result")
	}
	missing.Store(false)
	duplicate.Store(true)
	if _, e = p.FindDraft(ctx, d); e != provider.ErrConflict {
		t.Fatal("ambiguous result")
	}
	fail.Store(true)
	if _, e = p.CreateDraft(ctx, d); e == nil || posts.Load() != 2 {
		t.Fatal("write retried or failure hidden")
	}
	for _, mutate := range []func(*provider.DraftInput){func(d *provider.DraftInput) { d.Subject = "hi\r\nBcc: attacker@example.test" }, func(d *provider.DraftInput) { d.Recipients[0].Address = "bad\r\n" }, func(d *provider.DraftInput) { d.MessageID = "<foreign@example.test>" }, func(d *provider.DraftInput) { d.InReplyTo = "bad" }, func(d *provider.DraftInput) { d.BodyHTML = new(string) }, func(d *provider.DraftInput) { d.BodyText = strings.Repeat("a", 16385) }, func(d *provider.DraftInput) { d.ThreadID = "../bad" }} {
		bad := testDraft()
		mutate(&bad)
		if _, e = p.CreateDraft(ctx, bad); e != provider.ErrInvalidArgument {
			t.Fatal("unsafe draft accepted")
		}
	}
	if posts.Load() != 2 {
		t.Fatal("invalid input dispatched")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = p.CreateDraft(cancelled, d); e != context.Canceled {
		t.Fatal("cancellation ignored")
	}
}
