package app

import (
	"context"
	"errors"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

// These operations own the durable Gmail draft journal. Generated text alone
// never reaches this path: an immutable reviewed action and approval are required.
func (s *ReplyService) create(ctx context.Context, a domain.AccountID, action reply.Action, r ReplyResponse) ReplyResponse {
	fail := func(e error) ReplyResponse {
		result := actionReply(r, action)
		result.Code = aiWorkflowCode(e)
		return result
	}
	if s.gate != nil {
		select {
		case s.gate <- struct{}{}:
			defer func() { <-s.gate }()
		case <-ctx.Done():
			return fail(ctx.Err())
		}
	}
	p := s.factory(a)
	if !p.Capabilities().CreateDrafts {
		return fail(provider.ErrUnsupported)
	}
	if e := s.fresh(ctx, a, action.Plan.Sources); e != nil {
		return fail(e)
	}
	selected, e := s.emails.Get(ctx, a, action.Plan.EmailID)
	if e != nil {
		return fail(e)
	}
	live, e := p.FetchMessage(ctx, selected.Email.ProviderID)
	if e != nil {
		return fail(e)
	}
	// Check the exact selected message at the provider, including reply routing.
	if live.AccountID != a || live.ProviderID != selected.Email.ProviderID || live.ProviderThreadID != action.Plan.Draft.ThreadID || selected.Email.ProviderThreadID != action.Plan.Draft.ThreadID {
		return fail(provider.ErrMalformed)
	}
	live.ID = selected.Email.ID
	h, e := repository.EmailContentHash(live)
	if e != nil || h != selected.ContentHash {
		return fail(repository.ErrConflict)
	}
	if e = s.store.Transition(ctx, a, action.Plan.ID, "preview", "in_flight", provider.DraftReference{}, "", false); e != nil {
		return fail(e)
	}
	ref, e := p.CreateDraft(ctx, action.Plan.Draft)
	state, code := "created", ""
	if e != nil || ref.AccountID != a || ref.ProviderDraftID == "" || ref.ProviderMessageID == "" {
		state = "uncertain"
		code = "outcome_unknown"
		ref = provider.DraftReference{}
	}
	persist, done := context.WithTimeout(context.WithoutCancel(s.root), 5*time.Second)
	defer done()
	if err := s.store.Transition(persist, a, action.Plan.ID, "in_flight", state, ref, code, false); err != nil {
		state = "uncertain"
		code = "journal_unavailable"
	}
	action.State = state
	action.Code = code
	action.Reference = ref
	return actionReply(r, action)
}
func (s *ReplyService) reconcile(ctx context.Context, a domain.AccountID, action reply.Action, r ReplyResponse) ReplyResponse {
	var p provider.DraftFinder
	if s.finder != nil {
		p = s.finder(a)
	} else {
		p = s.factory(a)
	}
	ref, e := p.FindDraft(ctx, action.Plan.Draft)
	state, code := "created", ""
	if e != nil || ref.AccountID != a || ref.ProviderDraftID == "" || ref.ProviderMessageID == "" {
		state = "uncertain"
		code = "outcome_unknown"
		ref = action.Reference
		if errors.Is(e, provider.ErrConflict) {
			state = "conflict"
			code = "draft_changed"
		}
	}
	persist, done := context.WithTimeout(context.WithoutCancel(s.root), 5*time.Second)
	defer done()
	// A failed/cancelled provider read is not a completed reconciliation attempt.
	reconciled := e == nil || errors.Is(e, provider.ErrConflict) || errors.Is(e, provider.ErrNotFound)
	if err := s.store.Transition(persist, a, action.Plan.ID, action.State, state, ref, code, reconciled); err != nil {
		state = "uncertain"
		code = "journal_unavailable"
	}
	action.State = state
	action.Code = code
	action.Reference = ref
	action.Reconciled = reconciled
	return actionReply(r, action)
}
