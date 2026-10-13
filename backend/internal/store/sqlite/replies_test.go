package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

func draftPlan(t *testing.T, s *Store, a domain.AccountID) reply.Plan {
	t.Helper()
	ctx := context.Background()
	e := retrievalEmail(a, "draft-source", "Meeting", "Synthetic request", "sender@example.test", 12)
	e.ProviderThreadID = "thread"
	e.Headers = []domain.EmailHeader{{Name: "Message-ID", Value: "<parent@example.test>"}}
	saveEmail(t, s, e)
	row, err := s.Emails().Get(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, sources, draft, _, err := reply.Build([]repository.StoredEmail{row}, e.ID, "Professional", "")
	if err != nil {
		t.Fatal(err)
	}
	draft.BodyText = "Human-reviewed synthetic draft"
	draft.MessageID = "<mailpilot-plan@mailpilot.invalid>"
	p := reply.Plan{ID: "plan", AccountID: a, EmailID: e.ID, Sources: sources, Draft: draft, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	p.Digest = p.Hash()
	return p
}
func TestDraftJournalIsolationIntegrityAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	a := account(t, s, "draft-a")
	b := account(t, s, "draft-b")
	p := draftPlan(t, s, a)
	if e = s.Replies().Create(ctx, p); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Replies().Get(ctx, b, p.ID); !errors.Is(e, repository.ErrNotFound) {
		t.Fatal("cross-account plan")
	}
	if e = s.Replies().Transition(ctx, b, p.ID, "preview", "in_flight", provider.DraftReference{}, "", false); !errors.Is(e, repository.ErrNotFound) {
		t.Fatal("cross-account transition", e)
	}
	if _, e = s.db.Exec(`UPDATE reply_draft_actions SET plan_json='{}' WHERE id=?`, p.ID); e == nil {
		t.Fatal("mutable plan")
	}
	if _, e = s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, a, p.EmailID); e == nil {
		t.Fatal("journal source purged")
	}
	if e = s.Replies().Transition(ctx, a, p.ID, "preview", "in_flight", provider.DraftReference{}, "", false); e != nil {
		t.Fatal(e)
	}
	if e = s.Replies().Transition(ctx, a, p.ID, "preview", "in_flight", provider.DraftReference{}, "", false); !errors.Is(e, repository.ErrConflict) {
		t.Fatal("duplicate dispatch")
	}
	next := p
	next.ID = "next"
	next.Draft.MessageID = "<mailpilot-next@mailpilot.invalid>"
	next.Digest = next.Hash()
	if e = s.Replies().Create(ctx, next); !errors.Is(e, repository.ErrConflict) {
		t.Fatal("unresolved action ignored")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := s.Replies().Latest(ctx, a)
	if e != nil || restored.State != "in_flight" || restored.Plan.Digest != p.Digest {
		t.Fatal("lost uncertain outcome")
	}
	if e = s.Replies().Transition(ctx, a, p.ID, "in_flight", "resolved", provider.DraftReference{}, "", true); !errors.Is(e, repository.ErrConflict) {
		t.Fatal("resolve before reconcile")
	}
	if e = s.Replies().Transition(ctx, a, p.ID, "in_flight", "uncertain", provider.DraftReference{}, "outcome_unknown", true); e != nil {
		t.Fatal(e)
	}
	if e = s.Replies().Transition(ctx, a, p.ID, "uncertain", "resolved", provider.DraftReference{}, "manually_resolved", true); e != nil {
		t.Fatal(e)
	}
	if e = s.Replies().Create(ctx, next); e != nil {
		t.Fatal("reviewed resolution did not unblock", e)
	}
	row, _ := s.Emails().Get(ctx, a, p.EmailID)
	row.Email.BodyText = "Changed"
	saveEmail(t, s, row.Email)
	if e = s.Replies().Transition(ctx, a, next.ID, "preview", "in_flight", provider.DraftReference{}, "", false); !errors.Is(e, repository.ErrConflict) {
		t.Fatal("stale dispatch")
	}
}
func TestReplySourcesBoundedAndSettingsHaveNoKeys(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "a")
	b := account(t, s, "b")
	p := draftPlan(t, s, a)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		e := retrievalEmail(a, "earlier"+strings.Repeat("x", i), "Earlier", "Synthetic earlier context", "sender@example.test", 10)
		e.ProviderThreadID = "thread"
		saveEmail(t, s, e)
	}
	e := retrievalEmail(b, "foreign", "Private", "Not related", "foreign@example.test", 10)
	e.ProviderThreadID = "thread"
	saveEmail(t, s, e)
	rows, capped, err := s.Replies().Sources(ctx, a, p.EmailID, true)
	if err != nil || !capped || len(rows) != 5 || rows[4].Email.ID != p.EmailID {
		t.Fatal("unbounded or unordered thread", err)
	}
	for _, r := range rows {
		if r.Email.AccountID != a || r.Email.ProviderThreadID != "thread" {
			t.Fatal("foreign context")
		}
	}
	rows, capped, err = s.Replies().Sources(ctx, a, p.EmailID, false)
	if err != nil || capped || len(rows) != 1 {
		t.Fatal("selected message expanded")
	}
	if err = s.Replies().SaveSettings(ctx, a, reply.Settings{Model: "fixture-model", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Replies().Settings(ctx, b)
	if err != nil || cfg.Enabled {
		t.Fatal("cross-account settings")
	}
	var schema string
	if err = s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='reply_ai_settings'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"api_key", "token", "secret"} {
		if strings.Contains(schema, forbidden) {
			t.Fatal("credential column")
		}
	}
	bad := p
	bad.ID = "tampered"
	if err = s.Replies().Create(ctx, bad); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("invalid digest")
	}
}

func TestLatestReplyPreviewFollowsCreationOrder(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "preview-order")
	ctx := context.Background()
	p := draftPlan(t, s, a)
	for _, id := range []string{"zzz", "aaa"} {
		p.ID = id
		p.Draft.MessageID = "<mailpilot-" + id + "@mailpilot.invalid>"
		p.Digest = p.Hash()
		if err := s.Replies().Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		// Exercise ordering independent of clock resolution and random ID order.
		if _, err := s.db.Exec("UPDATE reply_draft_actions SET created_at=created_at+1000 WHERE account_id=? AND id=?", a, id); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.Replies().Latest(ctx, a)
	if err != nil || latest.Plan.ID != "aaa" {
		t.Fatal("restored superseded preview", err)
	}
}
