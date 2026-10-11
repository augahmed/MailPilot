package gmail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

func TestLabelWritesAreNarrowAndNeverRetried(t *testing.T) {
	var creates, modifies atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("authentication missing")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/labels" {
			json.NewEncoder(w).Encode(map[string]any{"labels": []any{map[string]string{"id": "INBOX", "name": "INBOX", "type": "system"}, map[string]string{"id": "other", "name": "Other", "type": "user"}, map[string]string{"id": "label", "name": "MailPilot/Receipts", "type": "user"}}})
			return
		}
		if r.Method != http.MethodPost {
			t.Error("unexpected destructive method")
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/labels" {
			creates.Add(1)
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "MailPilot/Receipts" {
				t.Error("bad label creation")
			}
			json.NewEncoder(w).Encode(map[string]string{"id": "label", "name": "MailPilot/Receipts", "type": "user"})
			return
		}
		if r.URL.Path != "/messages/email/modify" {
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
			return
		}
		modifies.Add(1)
		var body map[string][]string
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 1 || !reflect.DeepEqual(body["addLabelIds"], []string{"label"}) && !reflect.DeepEqual(body["removeLabelIds"], []string{"label"}) {
			t.Error("unsafe label delta")
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "email"})
	}))
	defer server.Close()
	p := NewProvider("a", syntheticTokens{})
	p.baseURL = server.URL
	p.client = server.Client()
	ctx := context.Background()
	if !p.Capabilities().CreateLabels || !p.Capabilities().ModifyLabels {
		t.Fatal("capabilities not advertised")
	}
	if _, err := p.CreateLabel(ctx, "MailPilot/Receipts"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"INBOX", "other", "missing", "../label"} {
		if err := p.ApplyLabel(ctx, "email", domain.ProviderLabelID(id)); err != provider.ErrInvalidArgument {
			t.Fatal("unsafe label allowed", id, err)
		}
	}
	if err := p.ApplyLabel(ctx, "email", "label"); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveLabel(ctx, "email", "label"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if err := p.ApplyLabel(ctx, "email", "label"); err == nil {
		t.Fatal("HTTP failure accepted")
	}
	if modifies.Load() != 3 || creates.Load() != 1 {
		t.Fatal("write retried")
	}
	for _, name := range []string{"INBOX", "Other", "MailPilot/", "MailPilot/Bad\nName"} {
		if _, err := p.CreateLabel(ctx, name); err != provider.ErrInvalidArgument {
			t.Fatal("unsafe creation allowed")
		}
	}
}
