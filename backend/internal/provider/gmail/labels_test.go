package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

func TestLabelWritesAreNarrowAndNeverRetried(t *testing.T) {
	for _, name := range []string{"MailPilot/Receipts", domain.PotentialPhishingLabel} {
		t.Run(name, func(t *testing.T) {
			var creates, modifies atomic.Int32
			var fail atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
					t.Error("authentication missing")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/labels" {
					json.NewEncoder(w).Encode(map[string]any{"labels": []any{map[string]string{"id": "INBOX", "name": "INBOX", "type": "system"}, map[string]string{"id": "other", "name": "Other", "type": "user"}, map[string]string{"id": "label", "name": name, "type": "user"}}})
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
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != name {
						t.Error("bad label creation")
					}
					json.NewEncoder(w).Encode(map[string]string{"id": "label", "name": name, "type": "user"})
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
			if _, err := p.CreateLabel(ctx, name); err != nil {
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
			for _, name := range []string{"INBOX", "SPAM", "TRASH", "Other", "Potential Phishing/Other", "Potential Phishing ", "MailPilot/", "MailPilot/Bad\nName"} {
				if _, err := p.CreateLabel(ctx, name); err != provider.ErrInvalidArgument {
					t.Fatal("unsafe creation allowed")
				}
			}

		})
	}
}

func TestCreateLabelVerifiesOmittedTypeWithoutRetryingWrite(t *testing.T) {
	for _, mode := range []string{"verified", "wrong_id", "wrong_name", "system", "missing_type", "get_failed", "post_failed"} {
		t.Run(mode, func(t *testing.T) {
			var creates, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost && r.URL.Path == "/labels" {
					creates.Add(1)
					if mode == "post_failed" {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"id": "Label_created", "name": "MailPilot/Receipts"})
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/labels/Label_created" {
					t.Error("unexpected request")
					w.WriteHeader(404)
					return
				}
				reads.Add(1)
				if mode == "get_failed" {
					w.WriteHeader(404)
					return
				}
				label := map[string]string{"id": "Label_created", "name": "MailPilot/Receipts", "type": "user"}
				switch mode {
				case "wrong_id":
					label["id"] = "Label_other"
				case "wrong_name":
					label["name"] = "Other"
				case "system":
					label["type"] = "system"
				case "missing_type":
					delete(label, "type")
				}
				json.NewEncoder(w).Encode(label)
			}))
			defer server.Close()
			p := NewProvider("a", syntheticTokens{})
			p.baseURL = server.URL
			p.client = server.Client()
			label, err := p.CreateLabel(context.Background(), "MailPilot/Receipts")
			if mode == "verified" {
				if err != nil || label.ProviderID != "Label_created" || label.Kind != domain.LabelKindUser {
					t.Fatal("valid creation rejected", err)
				}
			} else if err == nil {
				t.Fatal("unverified creation accepted")
			}
			if mode == "system" && !errors.Is(err, provider.ErrMalformed) {
				t.Fatal("system label not rejected", err)
			}
			if creates.Load() != 1 {
				t.Fatal("POST was retried")
			}
			wantReads := int32(1)
			if mode == "post_failed" {
				wantReads = 0
			}
			if reads.Load() != wantReads {
				t.Fatal("unexpected verification count", reads.Load())
			}
		})
	}
}
