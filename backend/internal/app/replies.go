package app

import (
	"context"
	"crypto/rand"
	"sync"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
)

type ReplyService struct {
	mu           sync.Mutex
	root         context.Context
	session      func() (domain.AccountID, context.Context, error)
	store        reply.Store
	finder       func(domain.AccountID) provider.DraftFinder
	response     ReplyResponse
	cancel       context.CancelFunc
	busy, closed bool
	generation   uint64
	wg           sync.WaitGroup
}

func NewReplyMaintenance(root context.Context, session func() (domain.AccountID, context.Context, error), store reply.Store, finder func(domain.AccountID) provider.DraftFinder) *ReplyService {
	return &ReplyService{root: root, session: session, store: store, finder: finder, response: emptyReply()}
}
func (s *ReplyService) clearLocked() {
	if s.cancel != nil {
		s.cancel()
	}
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
	return a.Replies.HandleMaintenance(ctx, p)
}
func actionReply(base ReplyResponse, a reply.Action) ReplyResponse {
	base.ActionID = a.Plan.ID
	base.DraftID = a.Reference.ProviderDraftID
	base.To = a.Plan.Draft.Recipients[0].Address
	base.Subject = a.Plan.Draft.Subject
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

// Background work is joined on shutdown. Mailbox outcomes are persisted even
// when the account session disconnects; sensitive UI output is suppressed.
func (s *ReplyService) startLocked(session context.Context, state string, work func(context.Context, ReplyResponse) ReplyResponse) {
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
	}()
}
