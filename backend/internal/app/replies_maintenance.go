package app

import (
	"context"
	"errors"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

// ActiveValid is the desktop allowlist. Valid remains a historical contract
// validator until the retired implementation packages can be removed safely.
func (p ReplyRequest) ActiveValid() bool {
	switch p.Operation {
	case "status", "configure", "disable", "reconcile", "resolve", "cancel":
		return p.Valid()
	default:
		return false
	}
}

// NewReplyMaintenance composes settings and GET-only historical recovery. It has
// no email context reader, draft creator, or generation entry point.
func NewReplyMaintenance(root context.Context, session func() (domain.AccountID, context.Context, error), store reply.Store, settings ai.SettingsStore, finder func(domain.AccountID) provider.DraftFinder, keys func(domain.AccountID) credentials.SecretStore, factory func(domain.AccountID, string) (ai.AIProvider, ai.Identity, error)) *ReplyService {
	s := NewReplyService(root, session, store, settings, nil, nil, keys, factory)
	s.finder = finder
	s.maintenance = true
	return s
}

func (s *ReplyService) HandleMaintenance(ctx context.Context, p ReplyRequest) ReplyResponse {
	fail := func(code string) ReplyResponse { r := emptyReply(); r.Code = code; return r }
	if !p.ActiveValid() {
		return fail("validation")
	}
	// Reuse only the existing non-generating settings operations. They load/save
	// OS credentials and validate adapter construction, but never call AI.
	if p.Operation == "configure" || p.Operation == "disable" {
		return s.Handle(ctx, p)
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
		// Settings failure must not prevent historical recovery. Report it while
		// preserving the action and never load an AI key on this read path.
		cfg, settingsErr := s.settings.Settings(ctx, account)
		r.Configured, r.Model = cfg.Enabled, cfg.Model
		r.Provider, r.External = "", false
		if cfg.Enabled {
			r.Provider, r.External = "openai", true
		}
		a, e := s.store.Latest(ctx, account)
		if e == nil {
			r = maintenanceAction(r, a)
		} else if !errors.Is(e, repository.ErrNotFound) {
			return fail(aiWorkflowCode(e))
		}
		if settingsErr != nil {
			r.Code = aiWorkflowCode(settingsErr)
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
			return maintenanceResponse(s.reconcile(job, account, a, r))
		})
	}
	if session.Err() != nil {
		return fail("unauthorized")
	}
	return boundReply(s.response)
}

func maintenanceAction(r ReplyResponse, a reply.Action) ReplyResponse {
	return maintenanceResponse(actionReply(r, a))
}

func maintenanceResponse(r ReplyResponse) ReplyResponse {
	// Persisted preview bodies remain evidence, never a restored editing session.
	r.Context, r.Instruction, r.Policy = "", "", ""
	r.PreviewID, r.ReviewID, r.ExpiresAt = "", "", 0
	return r
}
