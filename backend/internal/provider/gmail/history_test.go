package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryProtocolPaginationDedupeAndExpiredCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("labelId") != "" || r.URL.Query().Get("historyTypes") != "" {
			t.Error("history must be read-only and unfiltered")
		}
		if r.URL.Path == "/profile" {
			w.Write([]byte(`{"historyId":"9007199254740993"}`))
			return
		}
		if r.URL.Query().Get("startHistoryId") == "98" {
			w.WriteHeader(400)
			return
		}
		if r.URL.Query().Get("startHistoryId") == "99" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("startHistoryId") != "100" {
			t.Error("changed start cursor during pagination")
		}
		if r.URL.Query().Get("pageToken") == "next" {
			w.Write([]byte(`{"historyId":"300","history":[{"id":"200","messagesDeleted":[{"message":{"id":"gone"}}]}]}`))
			return
		}
		w.Write([]byte(`{"historyId":"300","nextPageToken":"next","history":[{"id":"110","messages":[{"id":"m"}],"labelsAdded":[{"message":{"id":"m"},"labelIds":["INBOX"]}],"labelsRemoved":[{"message":{"id":"m"},"labelIds":["UNREAD"]}]}]}`))
	}))
	defer server.Close()
	p := NewProvider("a", syntheticTokens{})
	p.baseURL = server.URL
	p.client = server.Client()
	ctx := context.Background()
	cursor, err := p.CurrentCheckpoint(ctx)
	if err != nil || cursor != "9007199254740993" {
		t.Fatal("history ID lost precision", err)
	}
	first, err := p.ListChanges(ctx, "100", provider.PageOptions{Limit: 100})
	if err != nil || len(first.Records) != 1 || len(first.Records[0].Changes) != 1 || first.NextPageToken != "next" {
		t.Fatal("history mapping failed", err)
	}
	second, err := p.ListChanges(ctx, "100", provider.PageOptions{Limit: 100, PageToken: first.NextPageToken})
	if err != nil || !second.Records[0].Changes[0].Deleted || second.NextPageToken != "" {
		t.Fatal("deletion lost", err)
	}
	for _, cursor := range []string{"98", "99", "invalid", ""} {
		if _, err := p.ListChanges(ctx, cursor, provider.PageOptions{Limit: 100}); !errors.Is(err, provider.ErrCheckpointExpired) {
			t.Fatal("invalid checkpoint not recoverable")
		}
	}
}
func TestGmailIncrementalIntegrationKeepsBodyAndDoesNotRelist(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "gmail", ProviderAccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	lists, full, minimal, history := 0, 0, 0, 0
	original := fixture(t, "multipart")
	original.ID = "m1"
	original.LabelIDs = []string{"INBOX"}
	original.HistoryID = "100"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/profile":
			w.Write([]byte(`{"historyId":"100"}`))
		case "/labels":
			w.Write([]byte(`{"labels":[{"id":"INBOX","name":"Inbox","type":"system"},{"id":"STARRED","name":"Starred","type":"system"}]}`))
		case "/messages":
			lists++
			w.Write([]byte(`{"messages":[{"id":"m1","threadId":"t"}]}`))
		case "/history":
			history++
			if r.URL.Query().Get("startHistoryId") == "100" {
				w.Write([]byte(`{"historyId":"200","history":[{"id":"150","labelsRemoved":[{"message":{"id":"m1"},"labelIds":["INBOX"]}]}]}`))
			} else {
				w.Write([]byte(`{"historyId":"200"}`))
			}
		case "/messages/m1":
			m := original
			if history > 0 {
				m.LabelIDs = []string{"STARRED"}
				m.HistoryID = "150"
			}
			if r.URL.Query().Get("format") == "minimal" {
				minimal++
				m.Payload = part{}
			} else {
				full++
			}
			json.NewEncoder(w).Encode(m)
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s := mailsync.New(ctx, func() (domain.AccountID, context.Context, error) { return a, ctx, nil }, func(a domain.AccountID) mailsync.Reader {
		p := NewProvider(a, syntheticTokens{})
		p.baseURL = server.URL
		p.client = server.Client()
		return p
	}, db.Emails(), 10)
	s.SetCheckpoints(db.SyncCheckpoints())
	defer s.Clear()
	s.Start()
	if p := awaitSync(t, s); p.Errors != 0 {
		t.Fatalf("initial failed: %+v", p)
	}
	before, err := db.Emails().GetByProviderID(ctx, a, "m1")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	if p := awaitSync(t, s); p.Updated != 1 || p.Errors != 0 {
		t.Fatalf("delta failed: %+v", p)
	}
	s.Start()
	if p := awaitSync(t, s); p.Discovered != 0 {
		t.Fatal("repeated delta")
	}
	after, err := db.Emails().GetByProviderID(ctx, a, "m1")
	if err != nil || after.Email.ID != before.Email.ID || after.Email.BodyText != before.Email.BodyText || len(after.Email.Labels) != 1 || after.Email.Labels[0].ProviderID != "STARRED" {
		t.Fatal("cache reconciliation failed")
	}
	if lists != 1 || full != 1 || minimal != 2 || history != 2 {
		t.Fatalf("unnecessary requests: lists=%d full=%d metadata=%d history=%d", lists, full, minimal, history)
	}
}
