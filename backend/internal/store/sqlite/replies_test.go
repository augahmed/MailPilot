package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
	"testing"
	"time"
)

func draftPlan(t *testing.T, s *Store, a domain.AccountID) reply.Plan {
	t.Helper()
	ctx := context.Background()
	e := legacyEmail(a, "draft-source", "Meeting", "Synthetic request", "sender@example.test", 12)
	e.ProviderThreadID = "thread"
	saveEmail(t, s, e)
	row, err := s.Emails().Get(ctx, a, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := reply.Plan{ID: "plan", AccountID: a, EmailID: e.ID, Sources: []reply.Source{{ID: e.ID, Hash: row.ContentHash}}, Draft: provider.DraftInput{Recipients: []domain.EmailAddress{{Address: "sender@example.test"}}, Subject: "Re: Meeting", BodyText: "Historical human-reviewed draft", ThreadID: "thread", MessageID: "<mailpilot-plan@mailpilot.invalid>", InReplyTo: "<parent@example.test>", References: "<parent@example.test>"}, ExpiresAt: time.Now().UTC().Add(-time.Hour)}
	p.Digest = p.Hash()
	if !p.Valid() {
		t.Fatal("invalid historical fixture")
	}
	return p
}

// Tests import historical rows directly. Production exposes no plan-creation API.
func importHistoricalDraft(t *testing.T, s *Store, p reply.Plan, state string) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO reply_draft_actions(account_id,id,email_id,plan_json,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, p.AccountID, p.ID, p.EmailID, string(raw), state, nowMillis(), nowMillis()); err != nil {
		t.Fatal(err)
	}
	for _, source := range p.Sources {
		if _, err = s.db.Exec(`INSERT INTO reply_draft_sources VALUES(?,?,?)`, p.AccountID, p.ID, source.ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestHistoricalDraftJournalIsolationIntegrityAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := account(t, s, "a")
	b := account(t, s, "b")
	p := draftPlan(t, s, a)
	importHistoricalDraft(t, s, p, "in_flight")
	if _, err = s.Replies().Get(ctx, b, p.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross account")
	}
	if err = s.Replies().Transition(ctx, b, p.ID, "in_flight", "uncertain", provider.DraftReference{}, "", true); err != repository.ErrConflict {
		t.Fatal("cross-account transition", err)
	}
	if _, err = s.db.Exec(`UPDATE reply_draft_actions SET plan_json='{}' WHERE id=?`, p.ID); err == nil {
		t.Fatal("mutable journal")
	}
	if _, err = s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, a, p.EmailID); err == nil {
		t.Fatal("journal evidence purged")
	}
	if err = s.Replies().Transition(ctx, a, p.ID, "preview", "in_flight", provider.DraftReference{}, "", false); err != repository.ErrInvalid {
		t.Fatal("new dispatch allowed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.Replies().Latest(ctx, a)
	if err != nil || action.State != "in_flight" || action.Plan.Digest != p.Digest {
		t.Fatal("history lost", err)
	}
	if err = s.Replies().Transition(ctx, a, p.ID, "in_flight", "resolved", provider.DraftReference{}, "", true); err != repository.ErrConflict {
		t.Fatal("resolution bypassed reconcile")
	}
	if err = s.Replies().Transition(ctx, a, p.ID, "in_flight", "uncertain", provider.DraftReference{}, "outcome_unknown", true); err != nil {
		t.Fatal(err)
	}
	if err = s.Replies().Transition(ctx, a, p.ID, "uncertain", "resolved", provider.DraftReference{}, "manually_resolved", true); err != nil {
		t.Fatal(err)
	}
	action, err = s.Replies().Get(ctx, a, p.ID)
	if err != nil || action.Plan.Digest != p.Digest || action.State != "resolved" {
		t.Fatal("immutable evidence changed", err)
	}
}
