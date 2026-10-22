package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

// Exercises the real adapter, preview journal, verified writes and local cache;
// only the HTTP server and credential source are synthetic.
func TestOrganization25EmailsThroughGmailHTTPAndCache(t *testing.T) {
	for _, mode := range []string{"rules", "semantic_rules", "manual_primary", "changed_labels", "stale_assessment", "refreshed_source", "semantic_rules_refreshed_source"} {
		t.Run(mode, func(t *testing.T) { organizationHTTPFlow(t, mode) })
	}
}
func organizationHTTPFlow(t *testing.T, mode string) {
	manual := mode != "rules" && !strings.HasPrefix(mode, "semantic_rules")
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "gmail", ProviderAccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	memberships := map[string][]string{}
	for i := 0; i < 25; i++ {
		memberships[fmt.Sprintf("mail-%02d", i)] = []string{"INBOX"}
	}
	created, writes := 0, 0
	labels := []map[string]string{{"id": "INBOX", "name": "INBOX", "type": "system"}, {"id": "external", "name": "External", "type": "user"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("missing authentication")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/labels" {
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(map[string]any{"labels": labels})
				return
			}
			if r.Method == http.MethodPost {
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "MailPilot/Receipts" {
					t.Error("unexpected label creation")
					w.WriteHeader(400)
					return
				}
				created++
				label := map[string]string{"id": "category-label", "name": body["name"], "type": "user"}
				labels = append(labels, label)
				json.NewEncoder(w).Encode(label)
				return
			}
		}
		id := strings.TrimPrefix(r.URL.Path, "/messages/")
		if strings.HasSuffix(id, "/modify") && r.Method == http.MethodPost {
			id = strings.TrimSuffix(id, "/modify")
			if _, ok := memberships[id]; !ok {
				w.WriteHeader(404)
				return
			}
			var delta map[string][]string
			if json.NewDecoder(r.Body).Decode(&delta) != nil || len(delta) != 1 {
				t.Error("invalid label delta")
				w.WriteHeader(400)
				return
			}
			if add, ok := delta["addLabelIds"]; ok && slices.Equal(add, []string{"category-label"}) {
				memberships[id] = append(memberships[id], add...)
			} else if remove, ok := delta["removeLabelIds"]; ok && slices.Equal(remove, []string{"category-label"}) {
				memberships[id] = slices.DeleteFunc(memberships[id], func(label string) bool { return label == "category-label" })
			} else {
				t.Error("unsafe label delta")
				w.WriteHeader(400)
				return
			}
			writes++
			json.NewEncoder(w).Encode(map[string]string{"id": id})
			return
		}
		if ids, ok := memberships[id]; ok && r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"id": id, "threadId": id, "historyId": "1", "internalDate": "1700000000000", "labelIds": ids, "payload": map[string]any{"mimeType": "text/plain", "headers": []map[string]string{{"name": "From", "value": "Store <store@example.test>"}, {"name": "Subject", "value": "Your receipt"}}, "body": map[string]any{"data": base64.RawURLEncoding.EncodeToString([]byte("Payment received. Order number: TEST-1."))}}})
			return
		}
		t.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer server.Close()
	provider := NewProvider(account, syntheticTokens{})
	provider.baseURL = server.URL
	provider.client = server.Client()
	rules, err := classification.NewRuleBasedClassifier(classification.StarterConfig())
	if err != nil {
		t.Fatal(err)
	}
	var engine classification.ClassificationEngine = rules
	if mode != "rules" {
		profile := repository.IndexProfile{Model: repository.EmbeddingModel{ID: "synthetic", Version: "synthetic", Dimension: 2}, Fingerprint: strings.Repeat("a", 64), PreprocessingVersion: "synthetic", IndexVersion: "synthetic", CategoryRecipe: repository.CategoryRecipe}
		var fallback classification.ClassificationEngine
		if !manual {
			fallback, err = classification.SemanticRules()
			if err != nil {
				t.Fatal(err)
			}
		}
		engine, err = classification.NewSemanticClassifier(store.Semantic(), profile, fallback, classification.DefaultSemanticPolicy())
		if err != nil {
			t.Fatal(err)
		}
	}
	categories, err := store.Categories().List(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{}
	for i := 0; i < 25; i++ {
		email, err := provider.FetchMessage(ctx, domain.ProviderMessageID(fmt.Sprintf("mail-%02d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(mode, "refreshed_source") {
			email.BodyText = "Payment received. Order number: OLD-CACHED-1."
		}
		email.ID, err = store.Emails().Upsert(ctx, email)
		if err != nil {
			t.Fatal(err)
		}
		if manual {
			stored, err := store.Emails().Get(ctx, account, email.ID)
			if err != nil {
				t.Fatal(err)
			}
			var categoryID domain.CategoryID
			for _, category := range categories {
				if category.Name == "Receipts" {
					categoryID = category.ID
				}
			}
			if err = store.Semantic().SetPreference(ctx, account, email.ID, "assigned", categoryID, stored.ContentHash); err != nil {
				t.Fatal(err)
			}
		}
		run, err := engine.Classify(ctx, email, categories)
		if err != nil || len(run.Results) != 1 {
			t.Fatal("missing receipt prediction", err)
		}
		if err = store.Classifications().Replace(ctx, run); err != nil {
			t.Fatal(err)
		}
		selected = append(selected, string(email.ID))
	}
	if mode == "changed_labels" {
		mu.Lock()
		for id := range memberships {
			memberships[id] = append(memberships[id], "external")
		}
		mu.Unlock()
	}
	if mode == "stale_assessment" {
		for _, category := range categories {
			if category.Name == "Receipts" {
				category.Description += " Updated description."
				if _, err := store.Categories().Save(ctx, account, category); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	service := app.NewOrganization(ctx, func() (domain.AccountID, context.Context, error) { return account, ctx, nil }, func(domain.AccountID) app.OrganizationProvider { return provider }, store.Emails(), store.Categories(), store.Classifications(), store.Organization(), engine)
	defer service.Close()
	await := func(request app.OrganizationRequest) app.OrganizationResponse {
		t.Helper()
		result := service.Handle(ctx, request)
		deadline := time.Now().Add(15 * time.Second)
		for result.State == "running" && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			result = service.Handle(ctx, app.OrganizationRequest{Operation: "status", AccountID: string(account)})
		}
		if result.Code != "" || result.State == "running" {
			t.Fatalf("organization failed: %+v", result)
		}
		return result
	}
	preview := await(app.OrganizationRequest{Operation: "preview", AccountID: string(account), EmailIDs: selected})
	if preview.State != "preview" || len(preview.Rows) != 25 || len(preview.Steps) != 26 || created != 0 || writes != 0 {
		t.Fatalf("preview wrote or omitted selected mail: %+v", preview)
	}
	applied := await(app.OrganizationRequest{Operation: "apply", AccountID: string(account), ActionID: preview.ID, Digest: preview.Digest})
	if applied.State != "applied" || created != 1 || writes != 25 {
		t.Fatalf("HTTP apply failed: creates=%d writes=%d %+v", created, writes, applied)
	}
	for _, id := range selected {
		stored, err := store.Emails().Get(ctx, account, domain.EmailID(id))
		if err != nil || stored.Email.BodyText != "Payment received. Order number: TEST-1." || !slices.ContainsFunc(stored.Email.Labels, func(label domain.EmailLabel) bool { return label.Name == "MailPilot/Receipts" }) {
			t.Fatal("Gmail change not reflected in cache", id, err)
		}
	}
	status := await(app.OrganizationRequest{Operation: "status", AccountID: string(account)})
	for _, row := range status.Rows {
		if row.UpdatedLabels == nil || !slices.Contains(*row.UpdatedLabels, "MailPilot/Receipts") {
			t.Fatal("status lost verified labels", row)
		}
	}
	undo := await(app.OrganizationRequest{Operation: "undo_preview", AccountID: string(account)})
	if len(undo.Rows) != 25 {
		t.Fatal("undo omitted emails")
	}
	undone := await(app.OrganizationRequest{Operation: "apply", AccountID: string(account), ActionID: undo.ID, Digest: undo.Digest})
	if undone.State != "applied" || writes != 50 {
		t.Fatal("25-email undo failed", undone)
	}
	for _, id := range selected {
		stored, err := store.Emails().Get(ctx, account, domain.EmailID(id))
		if err != nil || !slices.ContainsFunc(stored.Email.Labels, func(label domain.EmailLabel) bool { return label.ProviderID == "INBOX" }) || slices.ContainsFunc(stored.Email.Labels, func(label domain.EmailLabel) bool { return label.ProviderID == "category-label" }) || (mode == "changed_labels" && !slices.ContainsFunc(stored.Email.Labels, func(label domain.EmailLabel) bool { return label.ProviderID == "external" })) {
			t.Fatal("undo changed unrelated labels", err)
		}
	}
}
