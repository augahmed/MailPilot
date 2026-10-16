package app

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

type ReplyProvider interface {
	provider.DraftCreator
	provider.DraftFinder
	FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error)
	Capabilities() provider.Capabilities
}
type ReplyService struct {
	mu           sync.Mutex
	root         context.Context
	session      func() (domain.AccountID, context.Context, error)
	store        reply.Store
	settings     ai.SettingsStore
	emails       repository.EmailRepository
	finder       func(domain.AccountID) provider.DraftFinder
	factory      func(domain.AccountID) ReplyProvider
	keys         func(domain.AccountID) credentials.SecretStore
	aiFactory    func(domain.AccountID, string) (ai.AIProvider, ai.Identity, error)
	consent      *ai.Service
	sources      []reply.Source
	selected     domain.EmailID
	skeleton     provider.DraftInput
	reviewID     string
	response     ReplyResponse
	cancel       context.CancelFunc
	maintenance  bool
	busy, closed bool
	generation   uint64
	wg           sync.WaitGroup
	gate         chan struct{}
}

func NewReplyService(root context.Context, session func() (domain.AccountID, context.Context, error), store reply.Store, settings ai.SettingsStore, emails repository.EmailRepository, factory func(domain.AccountID) ReplyProvider, keys func(domain.AccountID) credentials.SecretStore, aiFactory func(domain.AccountID, string) (ai.AIProvider, ai.Identity, error)) *ReplyService {
	return &ReplyService{root: root, session: session, store: store, settings: settings, emails: emails, factory: factory, keys: keys, aiFactory: aiFactory, response: emptyReply()}
}
func (s *ReplyService) SetOperationGate(gate chan struct{}) { s.gate = gate }
func (s *ReplyService) clearLocked() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.consent != nil {
		s.consent.Clear()
	}
	s.consent = nil
	s.sources = nil
	s.selected = ""
	s.skeleton = provider.DraftInput{}
	s.reviewID = ""
	s.generation++
	s.response = emptyReply()
}
func (s *ReplyService) Clear() { s.mu.Lock(); defer s.mu.Unlock(); s.clearLocked() }
func (s *ReplyService) Close() {
	s.mu.Lock()
	s.closed = true
	s.clearLocked()
	s.mu.Unlock()
	s.wg.Wait()
}
func (a *App) Reply(ctx context.Context, p ReplyRequest) ReplyResponse {
	if a.Replies == nil {
		r := emptyReply()
		r.Code = "unauthorized"
		return r
	}
	r := a.Replies.HandleMaintenance(ctx, p)
	return r
}
func (s *ReplyService) fresh(ctx context.Context, a domain.AccountID, sources []reply.Source) error {
	for _, src := range sources {
		e, err := s.emails.Get(ctx, a, src.ID)
		if err != nil {
			return err
		}
		if e.ContentHash != src.Hash {
			return repository.ErrConflict
		}
	}
	return ctx.Err()
}
func actionReply(base ReplyResponse, a reply.Action) ReplyResponse {
	base.Context = ""
	base.Instruction = ""
	base.Policy = ""
	base.PreviewID = a.Plan.Digest
	base.ExpiresAt = a.Plan.ExpiresAt.UnixMilli()
	base.ActionID = a.Plan.ID
	base.DraftID = a.Reference.ProviderDraftID
	base.To = a.Plan.Draft.Recipients[0].Address
	base.Subject = a.Plan.Draft.Subject
	base.Body = a.Plan.Draft.BodyText
	base.State = a.State
	base.Code = a.Code
	base.Reconciled = a.Reconciled
	if base.State == "preview" {
		base.State = "draft_preview"
	}
	if base.State == "in_flight" {
		base.State = "uncertain"
		base.Code = "outcome_unknown"
	}
	return base
}
func (s *ReplyService) Handle(ctx context.Context, p ReplyRequest) ReplyResponse {
	if s.maintenance && p.Operation != "configure" && p.Operation != "disable" {
		return s.HandleMaintenance(ctx, p)
	}
	fail := func(code string) ReplyResponse { r := emptyReply(); r.Code = code; return r }
	if !p.Valid() {
		return fail("validation")
	}
	account, session, e := s.session()
	if e != nil || account == "" || session == nil || session.Err() != nil {
		s.Clear()
		return fail("unauthorized")
	}
	if p.AccountID != "" && p.AccountID != string(account) {
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
	if s.response.AccountID == "" {
		s.response.AccountID = string(account)
	}
	cfg, e := s.settings.Settings(ctx, account)
	if e != nil {
		return fail(aiWorkflowCode(e))
	}
	s.response.Configured = cfg.Enabled
	s.response.Model = cfg.Model
	if cfg.Enabled {
		s.response.Provider = "openai"
		s.response.External = true
	} else {
		s.response.Provider = ""
		s.response.External = false
	}
	if p.Operation == "status" {
		if !s.busy && s.response.State == "idle" {
			a, err := s.store.Latest(ctx, account)
			if err == nil {
				s.response = actionReply(s.response, a)
				if a.State == "preview" {
					s.sources = append([]reply.Source{}, a.Plan.Sources...)
					s.selected = a.Plan.EmailID
					s.skeleton = a.Plan.Draft
					s.reviewID = rand.Text()
					s.response.ReviewID = s.reviewID
				}
			} else if !errors.Is(err, repository.ErrNotFound) {
				return fail("operation_failed")
			}
		}
		if session.Err() != nil {
			s.clearLocked()
			return fail("unauthorized")
		}
		return boundReply(s.response)
	}
	if p.Operation == "cancel" {
		if !s.busy || p.JobID != s.response.JobID {
			return fail("stale_preview")
		}
		s.clearLocked()
		return fail("cancelled")
	}
	if s.busy {
		return fail("busy")
	}
	if p.Operation == "configure" || p.Operation == "disable" {
		s.clearLocked()
		s.response.AccountID = string(account)
		s.startLocked(account, session, "configuring", func(job context.Context, r ReplyResponse) ReplyResponse {
			if p.Operation == "disable" {
				if e := s.settings.SaveSettings(job, account, ai.Settings{}); e != nil {
					r.Code = aiWorkflowCode(e)
				} else {
					r.Code = aiWorkflowCode(s.keys(account).Delete(job))
				}
				r.Configured = false
				r.Model = ""
				r.Provider = ""
				r.External = false
			} else {
				_, _, err := s.aiFactory(account, p.Model)
				if err != nil {
					r.Code = aiWorkflowCode(err)
					r.State = "error"
					return r
				}
				keys := s.keys(account)
				if !p.APIKey.Empty() {
					err = keys.Save(job, p.APIKey.Secret)
				} else {
					_, err = keys.Load(job)
				}
				if err == nil {
					err = s.settings.SaveSettings(job, account, ai.Settings{Model: p.Model, Enabled: true})
				}
				r.Code = aiWorkflowCode(err)
				if err == nil {
					r.Configured = true
					r.Model = p.Model
					r.Provider = "openai"
					r.External = true
				}
			}
			r.State = "idle"
			return r
		})
		return boundReply(s.response)
	}
	switch p.Operation {
	case "prepare":
		if !cfg.Enabled {
			return fail("ai_disabled")
		}
		rows, capped, err := s.store.Sources(ctx, account, domain.EmailID(p.EmailID), p.IncludeThread)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		for _, row := range rows {
			if row.Email.AccountID != account {
				return fail("unauthorized")
			}
		}
		payload, sources, skeleton, truncated, err := reply.Build(rows, domain.EmailID(p.EmailID), p.Tone, p.Instruction)
		if err != nil {
			return fail("context_unavailable")
		}
		engine, identity, err := s.aiFactory(account, cfg.Model)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		consent, err := ai.NewService(engine, identity)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		preview, err := consent.Prepare(ctx, account, ai.Draft, payload)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		if s.consent != nil {
			s.consent.Clear()
		}
		s.consent = consent
		s.sources = sources
		s.skeleton = skeleton
		s.selected = domain.EmailID(p.EmailID)
		s.reviewID = ""
		s.response = ReplyResponse{AccountID: string(account), State: "ai_preview", Configured: true, Provider: identity.Provider, Model: identity.Model, External: identity.External, PreviewID: preview.ID, ExpiresAt: preview.ExpiresAt.UnixMilli(), Context: payload.Context.Reveal(), Instruction: payload.Instruction.Reveal(), Policy: preview.Instructions.Reveal(), To: skeleton.Recipients[0].Address, Subject: skeleton.Subject, SourceCount: len(rows), Truncated: capped || truncated}
	case "generate":
		if s.consent == nil || s.response.State != "ai_preview" || p.PreviewID != s.response.PreviewID {
			return fail("stale_preview")
		}
		consent := s.consent
		sources := append([]reply.Source{}, s.sources...)
		s.startLocked(account, session, "generating", func(job context.Context, r ReplyResponse) ReplyResponse {
			err := s.fresh(job, account, sources)
			var result ai.Result
			if err == nil {
				result, err = consent.Execute(job, account, p.PreviewID, p.Approved)
			}
			r.Context = ""
			r.Instruction = ""
			r.Policy = ""
			r.PreviewID = ""
			r.ExpiresAt = 0
			if err == nil && len(result.Text.Reveal()) > reply.MaxBody {
				r.Code = "response_too_large"
				r.State = "error"
				return r
			}
			if err != nil {
				r.Code = aiWorkflowCode(err)
				r.State = "error"
				return r
			}
			r.Body = result.Text.Reveal()
			r.State = "editing"
			r.ReviewID = rand.Text()
			return r
		})
	case "preview_draft":
		if s.reviewID == "" || p.PreviewID != s.reviewID || (s.response.State != "editing" && s.response.State != "draft_preview") {
			return fail("stale_preview")
		}
		draft := s.skeleton
		draft.BodyText = p.Body
		id := rand.Text()
		draft.MessageID = "<mailpilot-" + id + "@mailpilot.invalid>"
		plan := reply.Plan{ID: id, AccountID: account, EmailID: s.selected, Sources: append([]reply.Source{}, s.sources...), Draft: draft, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
		plan.Digest = plan.Hash()
		if err := s.store.Create(ctx, plan); err != nil {
			return fail(aiWorkflowCode(err))
		}
		s.response = actionReply(s.response, reply.Action{Plan: plan, State: "preview"})
	case "create", "reconcile", "resolve", "undo_preview":
		action, err := s.store.Get(ctx, account, p.ActionID)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		if action.Plan.AccountID != account {
			return fail("unauthorized")
		}
		if p.Operation == "undo_preview" {
			r := actionReply(s.response, action)
			r.Code = "undo_unavailable"
			return boundReply(r)
		}
		if p.Operation == "resolve" {
			if !action.Reconciled {
				return fail("reconcile_required")
			}
			if err = s.store.Transition(ctx, account, p.ActionID, action.State, "resolved", action.Reference, "manually_resolved", true); err != nil {
				return fail(aiWorkflowCode(err))
			}
			action.State = "resolved"
			action.Code = "manually_resolved"
			s.response = actionReply(s.response, action)
			break
		}
		if p.Operation == "create" {
			if s.response.State != "draft_preview" || p.ActionID != s.response.ActionID || p.PreviewID != action.Plan.Digest || action.State != "preview" || !time.Now().Before(action.Plan.ExpiresAt) {
				return fail("stale_preview")
			}
			s.startLocked(account, session, "creating", func(job context.Context, r ReplyResponse) ReplyResponse { return s.create(job, account, action, r) })
		} else {
			if action.State != "in_flight" && action.State != "uncertain" && action.State != "conflict" {
				return fail("stale_preview")
			}
			s.startLocked(account, session, "reconciling", func(job context.Context, r ReplyResponse) ReplyResponse { return s.reconcile(job, account, action, r) })
		}
	}
	if session.Err() != nil {
		s.clearLocked()
		return fail("unauthorized")
	}
	return boundReply(s.response)
}

// Background work is joined on shutdown. Mailbox outcomes are persisted even
// when the account session disconnects; sensitive UI output is suppressed.
func (s *ReplyService) startLocked(account domain.AccountID, session context.Context, state string, work func(context.Context, ReplyResponse) ReplyResponse) {
	job, cancel := context.WithTimeout(s.root, 2*time.Minute)
	stop := context.AfterFunc(session, cancel)
	s.cancel = cancel
	s.busy = true
	s.response.State = state
	s.response.Code = ""
	s.response.JobID = rand.Text()
	base := s.response
	generation := s.generation
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer stop()
		r := work(job, base)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.cancel = nil
		if generation != s.generation || s.closed || session.Err() != nil {
			s.response = emptyReply()
			return
		}
		r.JobID = ""
		s.response = boundReply(r)
		if r.State == "editing" {
			s.reviewID = r.ReviewID
		}
	}()
}
