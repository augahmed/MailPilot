package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func testDraft() provider.DraftInput {
	return provider.DraftInput{Recipients: []domain.EmailAddress{{Address: "host@example.test"}}, Subject: "Re: Synthetic meeting", BodyText: "Human reviewed reply.\nThanks.", ThreadID: "thread", MessageID: "<mailpilot-test@mailpilot.invalid>", InReplyTo: "<parent@example.test>", References: "<parent@example.test>"}
}

// Always encode/fold subject text, including ASCII that resembles an encoded
// word. This preserves the exact reviewed subject without RFC header injection.
func draftSubject(subject string) string {
	var words []string
	for len(subject) > 0 {
		n := min(45, len(subject))
		for n < len(subject) && !utf8.RuneStart(subject[n]) {
			n--
		}
		words = append(words, "=?utf-8?B?"+base64.StdEncoding.EncodeToString([]byte(subject[:n]))+"?=")
		subject = subject[n:]
	}
	return strings.Join(words, "\r\n ")
}
func draftMIME(d provider.DraftInput, from string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + (&mail.Address{Address: from}).String() + "\r\n")
	b.WriteString("To: " + (&mail.Address{Name: d.Recipients[0].Name, Address: d.Recipients[0].Address}).String() + "\r\n")
	b.WriteString("Subject: " + draftSubject(d.Subject) + "\r\nMessage-ID: " + d.MessageID + "\r\nIn-Reply-To: " + d.InReplyTo + "\r\nReferences: " + d.References + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(canonicalBody(d.BodyText)))
	_ = w.Close()
	return b.Bytes()
}

func TestRetiredDraftCreationAndReadOnlyReconciliation(t *testing.T) {
	d := testDraft()
	var posts, reads atomic.Int32
	var edited, missing, duplicate atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("missing credentials")
		}
		if r.URL.Path == "/profile" && r.Method == "GET" {
			_, _ = io.WriteString(w, `{"emailAddress":"owner@example.test"}`)
			return
		}
		if r.Method != "GET" {
			posts.Add(1)
			t.Error("remote write during historical recovery")
			w.WriteHeader(405)
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
	if _, ok := any(p).(interface {
		CreateDraft(context.Context, provider.DraftInput) (provider.DraftReference, error)
	}); ok {
		t.Fatal("draft creator still exposed")
	}
	got, e := p.FindDraft(ctx, d)
	if e != nil || got.ProviderDraftID != "draft" || posts.Load() != 0 {
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
	for _, mutate := range []func(*provider.DraftInput){func(d *provider.DraftInput) { d.Subject = "hi\r\nBcc: attacker@example.test" }, func(d *provider.DraftInput) { d.Recipients[0].Address = "bad\r\n" }, func(d *provider.DraftInput) { d.MessageID = "<foreign@example.test>" }, func(d *provider.DraftInput) { d.InReplyTo = "bad" }, func(d *provider.DraftInput) { d.BodyHTML = new(string) }, func(d *provider.DraftInput) { d.BodyText = strings.Repeat("a", 16385) }, func(d *provider.DraftInput) { d.ThreadID = "../bad" }} {
		bad := testDraft()
		mutate(&bad)
		if _, e = p.FindDraft(ctx, bad); e != provider.ErrInvalidArgument {
			t.Fatal("unsafe draft accepted")
		}
	}
	if posts.Load() != 0 {
		t.Fatal("invalid input dispatched")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = p.FindDraft(cancelled, d); e != context.Canceled {
		t.Fatal("cancellation ignored")
	}
}
