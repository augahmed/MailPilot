package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
)

func TestGmailInboxSync500NewestAndDurableOlderBatches(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "gmail", ProviderAccountID: "a"}); err != nil {
		t.Fatal(err)
	}
	var full, minimal atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("sync attempted a remote mutation")
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/profile":
			json.NewEncoder(w).Encode(map[string]string{"historyId": "100"})
		case "/history":
			json.NewEncoder(w).Encode(map[string]string{"historyId": "100"})
		case "/labels":
			json.NewEncoder(w).Encode(map[string]any{"labels": []map[string]string{{"id": "INBOX", "name": "INBOX", "type": "system"}}})
		case "/messages":
			if r.URL.Query().Get("labelIds") != "INBOX" || r.URL.Query().Get("includeSpamTrash") != "false" {
				t.Error("Inbox scope changed")
			}
			start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
			size, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
			if size < 1 || size > 100 || start < 0 || start > 1250 {
				w.WriteHeader(400)
				return
			}
			end := min(start+size, 1250)
			refs := []map[string]string{}
			for i := start; i < end; i++ {
				refs = append(refs, map[string]string{"id": fmt.Sprintf("mail-%04d", i)})
			}
			next := ""
			if end < 1250 {
				next = strconv.Itoa(end)
			}
			json.NewEncoder(w).Encode(map[string]any{"messages": refs, "nextPageToken": next})
		default:
			id := strings.TrimPrefix(r.URL.Path, "/messages/")
			i, err := strconv.Atoi(strings.TrimPrefix(id, "mail-"))
			if err != nil || i < 0 || i >= 1250 {
				w.WriteHeader(404)
				return
			}
			m := lifecycleMessage(id, "Synthetic Inbox message for complete paging qualification.", true)
			m.InternalDate = strconv.Itoa((2000 - i) * 1000)
			if r.URL.Query().Get("format") == "minimal" {
				minimal.Add(1)
				m.Payload = part{}
			} else {
				full.Add(1)
			}
			json.NewEncoder(w).Encode(m)
		}
	}))
	defer server.Close()
	for run, want := range []int{500, 500, 250, 0} {
		// A fresh service and adapter per run prove that continuation is in SQLite.
		s := mailsync.New(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, func(a domain.AccountID) mailsync.Reader {
			p := NewProvider(a, syntheticTokens{})
			p.baseURL = server.URL
			p.client = server.Client()
			return p
		}, db.Emails(), 500)
		s.SetCheckpoints(db.SyncCheckpoints())
		s.Start()
		deadline := time.Now().Add(90 * time.Second)
		for s.Status().State == "running" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		progress := s.Status()
		s.Clear()
		if progress.State != "completed" || progress.Imported != want || progress.Errors != 0 {
			t.Fatalf("run=%d wrong progress: %+v", run, progress)
		}
		count := 0
		for offset := 0; offset < 1300; offset += 100 {
			rows, err := db.Emails().ListSummaries(ctx, "a", repository.Page{Limit: 100, Offset: offset})
			if err != nil {
				t.Fatal(err)
			}
			count += len(rows)
			if len(rows) < 100 {
				break
			}
		}
		if count != min((run+1)*500, 1250) {
			t.Fatalf("run=%d wrong cache count: %d", run, count)
		}
	}
	if full.Load() != 1250 || minimal.Load() != 0 {
		t.Fatalf("Inbox repeated existing downloads: full=%d minimal=%d", full.Load(), minimal.Load())
	}
}
