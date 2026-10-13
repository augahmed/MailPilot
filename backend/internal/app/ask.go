package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/ask"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/search"
)

type AskRequest struct {
	Operation string `json:"operation"`
	AccountID string `json:"accountId"`
	Question  string `json:"question"`
	Sender    string `json:"sender"`
	From      *int64 `json:"from"`
	Until     *int64 `json:"until"`
	PreviewID string `json:"previewId"`
	JobID     string `json:"jobId"`
	Approved  bool   `json:"approved"`
}

func (p AskRequest) filters() search.Filters {
	return search.Filters{Sender: p.Sender, From: p.From, Until: p.Until}
}
func (p AskRequest) Valid() bool {
	for _, v := range []string{p.AccountID, p.PreviewID, p.JobID} {
		if len(v) > 100 || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	if len(p.Question) > 1000 || !utf8.ValidString(p.Question) || strings.ContainsRune(p.Question+p.Sender, '\x00') || p.filters().Validate() != nil {
		return false
	}
	selection := p.Question == "" && p.Sender == "" && p.From == nil && p.Until == nil
	control := p.PreviewID == "" && p.JobID == "" && !p.Approved
	if p.Operation == "status" {
		return selection && control
	}
	if p.AccountID == "" {
		return false
	}
	switch p.Operation {
	case "prepare":
		return strings.TrimSpace(p.Question) != "" && control
	case "generate":
		return selection && p.PreviewID != "" && p.JobID == "" && p.Approved
	case "clear":
		return selection && control
	case "cancel":
		return selection && p.JobID != "" && p.PreviewID == "" && !p.Approved
	}
	return false
}

type AskResponse struct {
	AccountID   string       `json:"accountId"`
	State       string       `json:"state"`
	JobID       string       `json:"jobId"`
	PreviewID   string       `json:"previewId"`
	ExpiresAt   int64        `json:"expiresAt"`
	Provider    string       `json:"provider"`
	Model       string       `json:"model"`
	External    bool         `json:"external"`
	Context     string       `json:"context"`
	Instruction string       `json:"instruction"`
	Policy      string       `json:"policy"`
	Version     string       `json:"version"`
	Partial     bool         `json:"partial"`
	Truncated   bool         `json:"truncated"`
	Sources     []ask.Source `json:"sources"`
	Answer      ask.Answer   `json:"answer"`
	Code        string       `json:"code"`
}

func emptyAsk() AskResponse {
	return AskResponse{State: "idle", Version: ask.Version, Sources: []ask.Source{}, Answer: ask.Answer{Claims: []ask.Claim{}}}
}

type AskRetriever interface {
	Query(context.Context, domain.AccountID, string, int, search.Filters) (search.Result, error)
}
type AskSettings interface {
	Settings(context.Context, domain.AccountID) (reply.Settings, error)
}
type AskEmails interface {
	Get(context.Context, domain.AccountID, domain.EmailID) (repository.StoredEmail, error)
}
type AskService struct {
	mu           sync.Mutex
	root         context.Context
	session      func() (domain.AccountID, context.Context, error)
	retrieval    AskRetriever
	settings     AskSettings
	emails       AskEmails
	factory      func(domain.AccountID, string) (ai.AIProvider, ai.Identity, error)
	consent      *ai.Service
	response     AskResponse
	cancel       context.CancelFunc
	busy, closed bool
	generation   uint64
	wg           sync.WaitGroup
}

func NewAskService(root context.Context, session func() (domain.AccountID, context.Context, error), retrieval AskRetriever, settings AskSettings, emails AskEmails, factory func(domain.AccountID, string) (ai.AIProvider, ai.Identity, error)) *AskService {
	return &AskService{root: root, session: session, retrieval: retrieval, settings: settings, emails: emails, factory: factory, response: emptyAsk()}
}
func (s *AskService) clearLocked() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.consent != nil {
		s.consent.Clear()
	}
	s.consent = nil
	s.generation++
	s.response = emptyAsk()
}
func (s *AskService) Clear() { s.mu.Lock(); defer s.mu.Unlock(); s.clearLocked() }
func (s *AskService) Close() {
	s.mu.Lock()
	s.closed = true
	s.clearLocked()
	s.mu.Unlock()
	s.wg.Wait()
}
func (a *App) AskMail(ctx context.Context, p AskRequest) AskResponse {
	if a.Ask == nil {
		r := emptyAsk()
		r.Code = "unauthorized"
		return r
	}
	return a.Ask.Handle(ctx, p)
}
func (s *AskService) fresh(ctx context.Context, account domain.AccountID, sources []ask.Source) error {
	for _, src := range sources {
		e, err := s.emails.Get(ctx, account, domain.EmailID(src.EmailID))
		if err != nil {
			return err
		}
		if e.Email.AccountID != account || e.ContentHash != src.Hash {
			return repository.ErrConflict
		}
	}
	return ctx.Err()
}
func (s *AskService) Handle(ctx context.Context, p AskRequest) AskResponse {
	fail := func(code string) AskResponse { r := emptyAsk(); r.Code = code; return r }
	if !p.Valid() {
		return fail("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil {
		s.Clear()
		return fail("unauthorized")
	}
	if p.AccountID != "" && p.AccountID != string(account) {
		return fail("unauthorized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil {
		return fail("cancelled")
	}
	if session.Err() != nil {
		s.clearLocked()
		return fail("unauthorized")
	}
	if s.response.AccountID != "" && s.response.AccountID != string(account) {
		s.clearLocked()
	}
	s.response.AccountID = string(account)
	if p.Operation == "clear" {
		s.clearLocked()
		s.response.AccountID = string(account)
		return s.response
	}
	if p.Operation == "cancel" {
		if !s.busy || p.JobID != s.response.JobID {
			return fail("stale_preview")
		}
		s.clearLocked()
		return fail("cancelled")
	}
	if p.Operation == "status" {
		if s.response.State == "preview" && time.Now().UnixMilli() >= s.response.ExpiresAt {
			s.clearLocked()
			s.response.AccountID = string(account)
			s.response.Code = "stale_preview"
		}
		return s.response
	}
	if s.busy {
		return fail("busy")
	}
	cfg, err := s.settings.Settings(ctx, account)
	if err != nil {
		return fail("operation_failed")
	}
	if !cfg.Enabled {
		s.clearLocked()
		return fail("ai_disabled")
	}
	if p.Operation == "generate" {
		if s.consent == nil || s.response.State != "preview" || p.PreviewID != s.response.PreviewID || s.response.Model != cfg.Model {
			return fail("stale_preview")
		}
		consent := s.consent
		s.startLocked(account, session, "generating", func(job context.Context, r AskResponse) (AskResponse, *ai.Service) {
			err := s.fresh(job, account, r.Sources)
			var output ai.Result
			if err == nil {
				output, err = consent.Execute(job, account, p.PreviewID, p.Approved)
			}
			r.Context = ""
			r.Policy = ""
			r.Instruction = ""
			r.PreviewID = ""
			r.ExpiresAt = 0
			if err == nil {
				err = s.fresh(job, account, r.Sources)
			}
			if err == nil {
				r.Answer, err = ask.ValidateAnswer(output.Text.Reveal(), r.Sources)
			}
			if err != nil {
				r.Code = replyCode(err)
				r.State = "error"
				r.Answer = ask.Answer{Insufficient: true, Claims: []ask.Claim{}}
			} else {
				r.State = "answered"
			}
			consent.Clear()
			return r, nil
		})
	} else {
		s.clearLocked()
		s.response.AccountID = string(account)
		s.startLocked(account, session, "retrieving", func(job context.Context, r AskResponse) (AskResponse, *ai.Service) {
			result, err := s.retrieval.Query(job, account, p.Question, 10, p.filters())
			if err != nil {
				r.State = "error"
				r.Code = searchCode(err)
				return r, nil
			}
			payload, sources, truncated, err := ask.Build(account, p.Question, result)
			r.Partial = result.Partial
			r.Truncated = truncated
			r.Sources = sources
			if err != nil {
				r.State = "error"
				r.Code = replyCode(err)
				r.Sources = []ask.Source{}
				return r, nil
			}
			if len(sources) == 0 {
				r.State = "answered"
				r.Answer.Insufficient = true
				return r, nil
			}
			engine, identity, err := s.factory(account, cfg.Model)
			if err != nil {
				r.State = "error"
				r.Code = replyCode(err)
				return r, nil
			}
			consent, err := ai.NewService(engine, identity)
			if err != nil {
				r.State = "error"
				r.Code = replyCode(err)
				return r, nil
			}
			preview, err := consent.Prepare(job, account, ai.Answer, payload)
			if err != nil {
				r.State = "error"
				r.Code = replyCode(err)
				return r, nil
			}
			r.State = "preview"
			r.PreviewID = preview.ID
			r.ExpiresAt = preview.ExpiresAt.UnixMilli()
			r.Provider = identity.Provider
			r.Model = identity.Model
			r.External = identity.External
			r.Context = payload.Context.Reveal()
			r.Instruction = payload.Instruction.Reveal()
			r.Policy = preview.Instructions.Reveal()
			return r, consent
		})
	}
	return s.response
}
func (s *AskService) startLocked(account domain.AccountID, session context.Context, state string, work func(context.Context, AskResponse) (AskResponse, *ai.Service)) {
	job, cancel := context.WithTimeout(s.root, 2*time.Minute)
	stop := context.AfterFunc(session, cancel)
	s.cancel = cancel
	s.busy = true
	s.response.State = state
	s.response.JobID = rand.Text()
	base := s.response
	g := s.generation
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer stop()
		r, consent := work(job, base)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.cancel = nil
		if g != s.generation || s.closed || session.Err() != nil || job.Err() != nil {
			if consent != nil {
				consent.Clear()
			}
			if g == s.generation {
				s.response = emptyAsk()
				s.response.Code = "cancelled"
			}
			return
		}
		r.JobID = ""
		b, err := json.Marshal(r)
		if err != nil || len(b) > 48*1024 {
			if consent != nil {
				consent.Clear()
			}
			r = emptyAsk()
			r.Code = "response_too_large"
			consent = nil
		}
		s.response = r
		s.consent = consent
	}()
}
