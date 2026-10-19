package app

import (
	"context"
	"errors"

	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

func (s *ReplyService) HandleMaintenance(ctx context.Context, p ReplyRequest) ReplyResponse {
	fail := func(code string) ReplyResponse { r := emptyReply(); r.Code = code; return r }
	if !p.Valid() {
		return fail("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || (p.AccountID != "" && p.AccountID != string(account)) {
		s.Clear()
		return fail("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fail("cancelled")
	}
	if s.response.AccountID != "" && s.response.AccountID != string(account) {
		s.clearLocked()
	}
	s.response.AccountID = string(account)
	if p.Operation == "cancel" {
		if !s.busy || p.JobID != s.response.JobID {
			return fail("stale_preview")
		}
		s.clearLocked()
		return fail("cancelled")
	}
	if s.busy {
		return boundReply(s.response)
	}
	if p.Operation == "status" {
		r := s.response
		a, e := s.store.Latest(ctx, account)
		if e == nil {
			r = maintenanceAction(r, a)
		} else if !errors.Is(e, repository.ErrNotFound) {
			return fail(aiWorkflowCode(e))
		}
		if session.Err() != nil {
			return fail("unauthorized")
		}
		s.response = r
		return boundReply(r)
	}
	a, err := s.store.Get(ctx, account, p.ActionID)
	if err != nil {
		return fail(aiWorkflowCode(err))
	}
	if a.Plan.AccountID != account {
		return fail("unauthorized")
	}
	if p.Operation == "resolve" {
		if !a.Reconciled {
			return fail("reconcile_required")
		}
		if err := s.store.Transition(ctx, account, p.ActionID, a.State, "resolved", a.Reference, "manually_resolved", true); err != nil {
			return fail(aiWorkflowCode(err))
		}
		a.State, a.Code = "resolved", "manually_resolved"
		s.response = maintenanceAction(s.response, a)
	} else {
		if a.State != "in_flight" && a.State != "uncertain" && a.State != "conflict" {
			return fail("stale_preview")
		}
		s.startLocked(account, session, "reconciling", func(job context.Context, r ReplyResponse) ReplyResponse {
			return s.reconcile(job, account, a, r)
		})
	}
	if session.Err() != nil {
		return fail("unauthorized")
	}
	return boundReply(s.response)
}

func maintenanceAction(r ReplyResponse, a reply.Action) ReplyResponse {
	return actionReply(r, a)
}
