package app

import (
	"context"
	"errors"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
)

// Reconciliation reads Gmail and updates only existing journal outcomes.
func (s *ReplyService) reconcile(ctx context.Context, a domain.AccountID, action reply.Action, r ReplyResponse) ReplyResponse {
	p := s.finder(a)
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
