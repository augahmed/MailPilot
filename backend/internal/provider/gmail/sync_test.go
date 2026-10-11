package gmail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
)

type syntheticTokens struct{}

func (syntheticTokens) AccessToken(context.Context, domain.AccountID) (credentials.Secret, error) {
	return credentials.NewSecret("synthetic-test-token"), nil
}
func awaitSync(t *testing.T, s *mailsync.Service) mailsync.Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Status()
		if p.State != "running" {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("sync did not finish")
	return mailsync.Progress{}
}
func TestSyncPaginationReplayAndMetadataReuse(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "gmail", ProviderAccountID: "a", Address: domain.EmailAddress{Address: "a@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	var full, minimal, lists atomic.Int32
	original := fixture(t, "multipart")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("remote mutation attempted")
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("missing authentication")
		}
		switch r.URL.Path {
		case "/labels":
			json.NewEncoder(w).Encode(map[string]any{"labels": []any{map[string]string{"id": "INBOX", "name": "Inbox", "type": "system"}}})
		case "/messages":
			lists.Add(1)
			if r.URL.Query().Get("labelIds") != "INBOX" {
				t.Error("not inbox scoped")
			}
			id, next := "m1", "page2"
			if r.URL.Query().Get("pageToken") == "page2" {
				id, next = "m2", "page3"
			}
			json.NewEncoder(w).Encode(map[string]any{"messages": []any{map[string]string{"id": id, "threadId": "t1"}}, "nextPageToken": next})
		default:
			if !strings.HasPrefix(r.URL.Path, "/messages/") || strings.Contains(r.URL.Path, "attachments") {
				t.Error("unexpected endpoint")
				w.WriteHeader(404)
				return
			}
			m := original
			m.ID = strings.TrimPrefix(r.URL.Path, "/messages/")
			if r.URL.Query().Get("format") == "minimal" {
				minimal.Add(1)
				m.Payload = part{}
				m.LabelIDs = []string{"INBOX"}
			} else {
				full.Add(1)
			}
			json.NewEncoder(w).Encode(m)
		}
	}))
	defer server.Close()
	factory := func(id domain.AccountID) mailsync.Reader {
		p := NewProvider(id, syntheticTokens{})
		p.baseURL = server.URL
		p.client = server.Client()
		return p
	}
	s := mailsync.New(ctx, func() (domain.AccountID, context.Context, error) { return a, ctx, nil }, factory, db.Emails(), 2)
	defer s.Clear()
	s.Start()
	p := awaitSync(t, s)
	if p.Imported != 2 || p.Discovered != 2 || p.Errors != 0 || !p.Capped {
		t.Fatalf("first sync: %+v", p)
	}
	first, err := db.Emails().GetByProviderID(ctx, a, "m1")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	p = awaitSync(t, s)
	if p.Imported != 0 || p.Updated != 2 || p.Errors != 0 || full.Load() != 2 || minimal.Load() != 2 || lists.Load() != 4 {
		t.Fatalf("replay: %+v, full=%d minimal=%d", p, full.Load(), minimal.Load())
	}
	stored, err := db.Emails().GetByProviderID(ctx, a, "m1")
	if err != nil || stored.Email.ID != first.Email.ID || stored.Email.BodyText != first.Email.BodyText || len(stored.Email.Labels) != 1 {
		t.Fatal("replay failed to preserve content/update labels", err)
	}
	rows, err := db.Emails().ListSummaries(ctx, a, repository.Page{Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("duplicate imports", err)
	}
	rows, err = db.Emails().ListSummaries(ctx, "other", repository.Page{Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatal("cross-account mailbox leak", err)
	}
}
