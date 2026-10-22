package gmail

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/domain"
)

// Incremental sync can legitimately reuse a complete cached body. Preview must
// prepare a fresh snapshot instead of asking the user to repeat that same sync.
func TestOrganizationPreviewRefreshesSyncedSelectedSnapshots(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			const latestBody = "Current original content returned by a full Gmail read."
			messages := []message{}
			for i := 0; i < count; i++ {
				messages = append(messages, lifecycleMessage(fmt.Sprintf("selected-%d", i), latestBody, true))
			}
			h := newLifecycle(t, messages...)
			ctx := context.Background()
			// Model a complete cache from an older decoder. Gmail's content is
			// unchanged; matching provider identity lets sync retain this cache.
			for _, m := range messages {
				email, err := normalize("a", m, nil)
				if err != nil {
					t.Fatal(err)
				}
				email.BodyText = "An earlier complete cached representation of this message."
				if _, err := h.db.Emails().Upsert(ctx, email); err != nil {
					t.Fatal(err)
				}
			}
			h.syncRun()
			if result := h.indexRun("start"); result.Status != "complete" {
				t.Fatalf("index setup failed: %+v", result)
			}
			categories, err := h.db.Categories().List(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			selected := []string{}
			previous := map[domain.EmailID]string{}
			for _, m := range messages {
				stored := h.stored(m.ID)
				previous[stored.Email.ID] = stored.ContentHash
				if err := h.db.Semantic().SetPreference(ctx, "a", stored.Email.ID, "assigned", categories[0].ID, stored.ContentHash); err != nil {
					t.Fatal(err)
				}
				run, err := h.engine.Classify(ctx, stored.Email, categories)
				if err != nil {
					t.Fatal(err)
				}
				if err := h.db.Classifications().Replace(ctx, run); err != nil {
					t.Fatal(err)
				}
				selected = append(selected, string(stored.Email.ID))
			}
			h.remote.mu.Lock()
			beforeFull := h.remote.full
			h.remote.mu.Unlock()
			h.syncRun()
			h.remote.mu.Lock()
			afterFull := h.remote.full
			h.remote.mu.Unlock()
			if afterFull != beforeFull {
				t.Fatal("fixture did not exercise complete-body reuse during sync")
			}
			for _, m := range messages {
				if h.stored(m.ID).Email.BodyText == latestBody {
					t.Fatal("sync unexpectedly replaced cached source")
				}
			}
			beforeInference := h.embedder.count()
			service := app.NewOrganization(ctx, lifecycleSession, func(a domain.AccountID) app.OrganizationProvider {
				p := NewProvider(a, syntheticTokens{})
				p.baseURL = h.server.URL
				p.client = h.server.Client()
				return p
			}, h.db.Emails(), h.db.Categories(), h.db.Classifications(), h.db.Organization(), h.engine)
			defer service.Close()
			result := service.Handle(ctx, app.OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: selected})
			deadline := time.Now().Add(15 * time.Second)
			for result.State == "running" && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
				result = service.Handle(ctx, app.OrganizationRequest{Operation: "status", AccountID: "a"})
			}
			if result.Code != "" || result.State != "preview" || len(result.Rows) != count {
				t.Fatalf("synced selection cannot progress to preview: %+v", result)
			}
			for _, m := range messages {
				stored := h.stored(m.ID)
				if stored.Email.BodyText != latestBody || stored.ContentHash == previous[stored.Email.ID] {
					t.Fatal("preview did not refresh selected source")
				}
				run, err := h.db.Classifications().Get(ctx, "a", stored.Email.ID)
				if err != nil || run.Semantic == nil || run.Semantic.SourceRevision != stored.ContentHash {
					t.Fatal("preview retained a stale assessment", err)
				}
				snapshot, err := h.db.Semantic().Snapshot(ctx, "a", stored.Email.ID, h.profile)
				if err != nil || snapshot.Availability == "ready" {
					t.Fatal("preview reused vectors from older content", err)
				}
			}
			if h.embedder.count() != beforeInference {
				t.Fatal("preview started embedding inference")
			}
		})
	}
}

func TestAutomaticSuggestionsThroughGmailRefreshWithoutManualPreferences(t *testing.T) {
	for _, mode := range []string{"single", "choose", "highest"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			h := newLifecycle(t, lifecycleMessage("one", "The project budget and delivery milestones are ready for discussion at the planning meeting next week.", true), lifecycleMessage("two", "The project schedule includes requirements, review meetings, implementation updates, and the latest delivery milestones.", true))
			h.syncRun()
			cats, err := h.db.Categories().List(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "single" {
				for _, cat := range cats[1:] {
					cat.Enabled = false
					if _, err := h.db.Categories().Save(ctx, "a", cat); err != nil {
						t.Fatal(err)
					}
				}
			}
			if p := h.indexRun("start"); p.Status != "complete" {
				t.Fatal(p)
			}
			chunker, err := app.NewCachedChunker(h.db.Emails(), h.db.Chunks())
			if err != nil {
				t.Fatal(err)
			}
			org := app.NewOrganization(ctx, lifecycleSession, func(a domain.AccountID) app.OrganizationProvider {
				p := NewProvider(a, syntheticTokens{})
				p.baseURL = h.server.URL
				p.client = h.server.Client()
				return p
			}, h.db.Emails(), h.db.Categories(), h.db.Classifications(), h.db.Organization(), h.engine)
			org.SetChunkProcessor(chunker.Prepare)
			defer org.Close()
			request := app.OrganizationRequest{Operation: "preview", AccountID: "a"}
			if mode == "highest" {
				request.SuggestionMode = "highest"
			}
			expected := map[string]string{}
			for _, id := range []string{"one", "two"} {
				stored := h.stored(id)
				run, err := h.db.Classifications().Get(ctx, "a", stored.Email.ID)
				if err != nil || len(run.Results) == 0 {
					t.Fatal("missing automatic suggestion", err)
				}
				chosen := run.Results[0]
				if mode == "choose" {
					options := org.Handle(ctx, app.OrganizationRequest{Operation: "suggestions", AccountID: "a", EmailIDs: []string{string(stored.Email.ID)}})
					if options.Code != "" || len(options.Rows[0].Suggestions) != 6 {
						t.Fatal("missing suggested options", options)
					}
					chosen = run.Results[2]
					request.Choices = append(request.Choices, app.OrganizationChoice{EmailID: string(stored.Email.ID), CategoryID: string(chosen.CategoryID)})
				}
				cat, err := h.db.Categories().Get(ctx, "a", chosen.CategoryID)
				if err != nil {
					t.Fatal(err)
				}
				expected[string(stored.Email.ID)] = "MailPilot/" + cat.Name
				request.EmailIDs = append(request.EmailIDs, string(stored.Email.ID))
				h.remote.mu.Lock()
				m := h.remote.messages[id]
				m.Payload.Headers = append(m.Payload.Headers, header{Name: "X-Transport-Revision", Value: "fresh"})
				h.remote.messages[id] = m
				h.remote.mu.Unlock()
			}
			calls := h.embedder.count()
			result := org.Handle(ctx, request)
			deadline := time.Now().Add(15 * time.Second)
			for result.State == "running" && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
				result = org.Handle(ctx, app.OrganizationRequest{Operation: "status", AccountID: "a"})
			}
			if result.Code != "" || result.State != "preview" || len(result.Rows) != 2 {
				t.Fatalf("automatic suggestions rejected: %+v", result)
			}
			for _, row := range result.Rows {
				if row.ProposedLabel != expected[row.EmailID] || row.ScoreKind != "cosine_similarity" {
					t.Fatal("wrong automatic category", row)
				}
				snapshot, err := h.db.Semantic().Snapshot(ctx, "a", domain.EmailID(row.EmailID), h.profile)
				if err != nil || snapshot.Availability != "ready" || snapshot.Preference.Mode != "" {
					t.Fatal("fresh automatic vectors were lost or manual preference created", err)
				}
			}
			if h.embedder.count() != calls {
				t.Fatal("organization ran embedding inference")
			}
		})
	}
}
