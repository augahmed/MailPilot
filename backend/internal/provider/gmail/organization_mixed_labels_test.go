package gmail

import (
	"context"
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

func TestOrganizationMixedLabelsWithFreshProviderPerOperation(t *testing.T) {
	for _, count := range []int{1, 3, 25} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("emails=%d/existing=%t", count, existing), func(t *testing.T) {
				ctx := context.Background()
				db, err := sqlite.Open(ctx, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "gmail", ProviderAccountID: "a"}); err != nil {
					t.Fatal(err)
				}
				names := []string{"Newsletters", "Jobs", "Receipts"}
				labels := map[string]map[string]string{"INBOX": {"id": "INBOX", "name": "INBOX", "type": "system"}}
				if existing {
					for i, name := range names {
						id := fmt.Sprintf("Label_%d", i)
						labels[id] = map[string]string{"id": id, "name": "MailPilot/" + name, "type": "user"}
					}
				}
				messages := map[string]message{}
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("mail-%02d", i)
					m := lifecycleMessage(id, "A synthetic forwarded message with an independently reviewed category.", true)
					m.LabelIDs = []string{"INBOX"}
					body := m.Payload
					m.Payload.MIMEType = "multipart/mixed"
					m.Payload.Body = part{}.Body
					m.Payload.Parts = []part{body}
					for j := 0; j < 7; j++ {
						attachment := part{PartID: fmt.Sprint(j + 1), MIMEType: "image/png", Filename: fmt.Sprintf("image-%d.png", j)}
						size := int64(100 + j)
						attachment.Body.Size = &size
						attachment.Body.AttachmentID = fmt.Sprintf("initial-%d", j)
						m.Payload.Parts = append(m.Payload.Parts, attachment)
					}
					messages[id] = m
				}
				var mu sync.Mutex
				creates, writes, reads := 0, 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/labels" {
						if r.Method == http.MethodGet {
							all := []map[string]string{}
							for _, l := range labels {
								all = append(all, l)
							}
							json.NewEncoder(w).Encode(map[string]any{"labels": all})
							return
						}
						if r.Method == http.MethodPost {
							var body map[string]string
							if json.NewDecoder(r.Body).Decode(&body) != nil {
								t.Error("bad label payload")
							}
							creates++
							id := fmt.Sprintf("Label_new_%d", creates)
							labels[id] = map[string]string{"id": id, "name": body["name"], "type": "user"}
							json.NewEncoder(w).Encode(map[string]string{"id": id, "name": body["name"]})
							return
						}
					}
					if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/labels/") {
						if label, ok := labels[strings.TrimPrefix(r.URL.Path, "/labels/")]; ok {
							json.NewEncoder(w).Encode(label)
							return
						}
					}
					id := strings.TrimPrefix(r.URL.Path, "/messages/")
					if r.Method == http.MethodPost && strings.HasSuffix(id, "/modify") {
						id = strings.TrimSuffix(id, "/modify")
						m, ok := messages[id]
						if !ok {
							w.WriteHeader(404)
							return
						}
						var delta map[string][]string
						if json.NewDecoder(r.Body).Decode(&delta) != nil || len(delta) != 1 || len(delta["addLabelIds"]) != 1 {
							t.Error("unsafe label delta")
							w.WriteHeader(400)
							return
						}
						m.LabelIDs = append(m.LabelIDs, delta["addLabelIds"][0])
						messages[id] = m
						writes++
						json.NewEncoder(w).Encode(map[string]any{"id": id, "labelIds": m.LabelIDs})
						return
					}
					if r.Method == http.MethodGet {
						if m, ok := messages[id]; ok {
							// Retrieval handles are transport metadata, not attachment
							// content identities. Change them on every repeated read.
							reads++
							m.Payload.Parts = append([]part{}, m.Payload.Parts...)
							for j := 1; j < len(m.Payload.Parts); j++ {
								m.Payload.Parts[j].Body.AttachmentID = fmt.Sprintf("read-%d-part-%d", reads, j)
							}
							json.NewEncoder(w).Encode(m)
							return
						}
					}
					t.Errorf("unexpected synthetic endpoint %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}))
				defer server.Close()
				factory := func(domain.AccountID) app.OrganizationProvider {
					p := NewProvider("a", syntheticTokens{})
					p.baseURL = server.URL
					p.client = server.Client()
					return p
				}
				profile := repository.IndexProfile{Model: repository.EmbeddingModel{ID: "synthetic", Version: "synthetic", Dimension: 2}, Fingerprint: strings.Repeat("a", 64), PreprocessingVersion: "synthetic", IndexVersion: "synthetic", CategoryRecipe: repository.CategoryRecipe}
				engine, err := classification.NewSemanticClassifier(db.Semantic(), profile, nil, classification.DefaultSemanticPolicy())
				if err != nil {
					t.Fatal(err)
				}
				cats, err := db.Categories().List(ctx, "a")
				if err != nil {
					t.Fatal(err)
				}
				selected := []string{}
				for i := 0; i < count; i++ {
					email, err := factory("a").FetchMessage(ctx, domain.ProviderMessageID(fmt.Sprintf("mail-%02d", i)))
					if err != nil {
						t.Fatal(err)
					}
					email.ID, err = db.Emails().Upsert(ctx, email)
					if err != nil {
						t.Fatal(err)
					}
					stored, err := db.Emails().Get(ctx, "a", email.ID)
					if err != nil {
						t.Fatal(err)
					}
					index := slices.IndexFunc(cats, func(c domain.EmailCategory) bool { return c.Name == names[i%len(names)] })
					if index < 0 {
						t.Fatal("missing category")
					}
					if err := db.Semantic().SetPreference(ctx, "a", email.ID, "assigned", cats[index].ID, stored.ContentHash); err != nil {
						t.Fatal(err)
					}
					run, err := engine.Classify(ctx, stored.Email, cats)
					if err != nil {
						t.Fatal(err)
					}
					if err := db.Classifications().Replace(ctx, run); err != nil {
						t.Fatal(err)
					}
					selected = append(selected, string(email.ID))
				}
				service := app.NewOrganization(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, factory, db.Emails(), db.Categories(), db.Classifications(), db.Organization(), engine)
				defer service.Close()
				await := func(request app.OrganizationRequest) app.OrganizationResponse {
					t.Helper()
					result := service.Handle(ctx, request)
					deadline := time.Now().Add(15 * time.Second)
					for result.State == "running" && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
						result = service.Handle(ctx, app.OrganizationRequest{Operation: "status", AccountID: "a"})
					}
					return result
				}
				preview := await(app.OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: selected})
				if preview.State != "preview" || preview.Code != "" {
					t.Fatalf("preview failed: %+v", preview)
				}
				result := await(app.OrganizationRequest{Operation: "apply", AccountID: "a", ActionID: preview.ID, Digest: preview.Digest})
				if result.State != "applied" || result.Code != "" || writes != count {
					t.Fatalf("confirmation failed: %+v, creates=%d writes=%d", result, creates, writes)
				}
				if existing && creates != 0 {
					t.Fatal("existing labels recreated")
				}
			})
		}
	}
}
