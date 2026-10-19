package app

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
	"strings"
	"testing"
	"time"
)

type historicalDrafts struct{ action reply.Action }

func (s *historicalDrafts) Get(_ context.Context, a domain.AccountID, id string) (reply.Action, error) {
	if a != s.action.Plan.AccountID || id != s.action.Plan.ID {
		return reply.Action{}, repository.ErrNotFound
	}
	return s.action, nil
}
func (s *historicalDrafts) Latest(ctx context.Context, a domain.AccountID) (reply.Action, error) {
	return s.Get(ctx, a, s.action.Plan.ID)
}
func (s *historicalDrafts) Transition(_ context.Context, a domain.AccountID, id, from, to string, ref provider.DraftReference, code string, reconciled bool) error {
	if a != s.action.Plan.AccountID || id != s.action.Plan.ID || s.action.State != from {
		return repository.ErrConflict
	}
	s.action.State = to
	s.action.Reference = ref
	s.action.Code = code
	s.action.Reconciled = reconciled
	return nil
}

type historicalFinder struct{ err error }

func (f historicalFinder) FindDraft(context.Context, provider.DraftInput) (provider.DraftReference, error) {
	return provider.DraftReference{AccountID: "a", ProviderDraftID: "existing", ProviderMessageID: "existing-message"}, f.err
}
func historicalRecovery(t *testing.T, state string, findErr error) *ReplyService {
	t.Helper()
	plan := reply.Plan{ID: "old", AccountID: "a", EmailID: "selected", Sources: []reply.Source{{ID: "selected", Hash: strings.Repeat("a", 64)}}, Draft: provider.DraftInput{Recipients: []domain.EmailAddress{{Address: "recipient@example.test"}}, Subject: "Historical", BodyText: "Historical reviewed body", ThreadID: "thread", MessageID: "<mailpilot-old@mailpilot.invalid>", InReplyTo: "<parent@example.test>", References: "<parent@example.test>"}, ExpiresAt: time.Now().Add(-time.Hour)}
	plan.Digest = plan.Hash()
	if !plan.Valid() {
		t.Fatal("bad fixture")
	}
	store := &historicalDrafts{action: reply.Action{Plan: plan, State: state}}
	ctx := context.Background()
	s := NewReplyMaintenance(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, store, func(domain.AccountID) provider.DraftFinder { return historicalFinder{err: findErr} })
	t.Cleanup(s.Close)
	return s
}
func waitRecovery(t *testing.T, s *ReplyService) ReplyResponse {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		r := s.HandleMaintenance(context.Background(), ReplyRequest{Operation: "status"})
		if r.State != "reconciling" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("recovery timed out")
	return ReplyResponse{}
}
func TestRecoveryNeverRestoresCreationOrAI(t *testing.T) {
	s := historicalRecovery(t, "preview", nil)
	r := s.HandleMaintenance(context.Background(), ReplyRequest{Operation: "status"})
	if r.State != "draft_preview" {
		t.Fatal("creatable preview restored")
	}
	for _, op := range []string{"configure", "disable", "prepare", "generate", "create", "preview_draft", "summarize_thread", "chat", "classify", "undo_preview"} {
		if r := s.HandleMaintenance(context.Background(), ReplyRequest{Operation: op, AccountID: "a"}); r.Code != "validation" {
			t.Fatal(op)
		}
	}
}
func TestRecoveryWorksWithoutAIOrKeysAndPreservesUncertainty(t *testing.T) {
	for _, tc := range []struct {
		err   error
		state string
	}{{nil, "created"}, {provider.ErrNotFound, "uncertain"}, {provider.ErrConflict, "conflict"}, {errors.New("private transport error"), "uncertain"}} {
		s := historicalRecovery(t, "in_flight", tc.err)
		ctx := context.Background()
		if r := s.HandleMaintenance(ctx, ReplyRequest{Operation: "resolve", AccountID: "a", ActionID: "old", Approved: true}); r.Code != "reconcile_required" {
			t.Fatal("premature resolution")
		}
		if r := s.HandleMaintenance(ctx, ReplyRequest{Operation: "reconcile", AccountID: "b", ActionID: "old"}); r.Code != "unauthorized" {
			t.Fatal("foreign recovery")
		}
		s.HandleMaintenance(ctx, ReplyRequest{Operation: "reconcile", AccountID: "a", ActionID: "old"})
		r := waitRecovery(t, s)
		if r.State != tc.state {
			t.Fatal(r.State, r.Code)
		}
		if tc.err == provider.ErrNotFound || tc.err == provider.ErrConflict {
			if !r.Reconciled {
				t.Fatal("reconciliation lost")
			}
			r = s.HandleMaintenance(ctx, ReplyRequest{Operation: "resolve", AccountID: "a", ActionID: "old", Approved: true})
			if r.State != "resolved" {
				t.Fatal(r.Code)
			}
		}
	}
}
