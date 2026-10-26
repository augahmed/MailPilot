package app

import (
	"context"
	"crypto/rand"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ProviderAdapter is trusted composition. Only registered functional adapters
// appear in Settings; none receive a mailbox reader, Gmail adapter or tools.
type ProviderAdapter struct {
	External bool
	New      func(domain.AccountID, string) (ai.AIProvider, error)
	Keys     func(domain.AccountID) credentials.SecretStore
}
type summaryEmailReader interface {
	Get(context.Context, domain.AccountID, domain.EmailID) (repository.StoredEmail, error)
}
type AIService struct {
	mu            sync.Mutex
	root          context.Context
	session       func() (domain.AccountID, context.Context, error)
	emails        summaryEmailReader
	settings      ai.SettingsStore
	providers     map[string]ProviderAdapter
	consent       *ai.Service
	snapshot      ai.SummaryRequest
	response      SummarizeEmailResponse
	cancel        context.CancelFunc
	busy, closed  bool
	generation    uint64
	wg            sync.WaitGroup
	answer        *questionState
	retriever     answerRetriever
	answerIndex   repository.IndexRepository
	answerWords   repository.KeywordRepository
	answerProfile repository.IndexProfile
	now           func() time.Time
}

func NewAIService(root context.Context, session func() (domain.AccountID, context.Context, error), emails summaryEmailReader, settings ai.SettingsStore, providers map[string]ProviderAdapter) *AIService {
	copy := make(map[string]ProviderAdapter, len(providers))
	for id, adapter := range providers {
		copy[id] = adapter
	}
	return &AIService{root: root, session: session, emails: emails, settings: settings, providers: copy, response: emptySummary(), now: time.Now}
}

// EnableAnswers reuses the exact same shared retriever/provider/store as search.
// Composition calls it once before IPC starts; no new index/queue is created.
func (s *AIService) EnableAnswers(search *retrieval.VectorSearch, index repository.IndexRepository) {
	s.retriever = search
	s.answerIndex = index
	s.answerWords = search.Keywords
	s.answerProfile = search.Profile
}
func (s *AIService) clearLocked() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.consent != nil {
		s.consent.Clear()
	}
	s.consent = nil
	s.snapshot = ai.SummaryRequest{}
	s.response = emptySummary()
	s.answer = nil
	s.generation++
}
func (s *AIService) Clear() { s.mu.Lock(); defer s.mu.Unlock(); s.clearLocked() }
func (s *AIService) Close() {
	s.mu.Lock()
	s.closed = true
	s.clearLocked()
	s.mu.Unlock()
	s.wg.Wait()
}
func (a *App) ManageAISettings(ctx context.Context, p AISettingsRequest) AISettingsResponse {
	if a.AI == nil {
		return AISettingsResponse{Providers: []AIProviderOption{}, Code: "unauthorized"}
	}
	return a.AI.Settings(ctx, p)
}
func (a *App) SummarizeEmail(ctx context.Context, p SummarizeEmailRequest) SummarizeEmailResponse {
	if a.AI == nil {
		r := emptySummary()
		r.Code = "unauthorized"
		return r
	}
	return a.AI.Summarize(ctx, p)
}
func (s *AIService) Settings(ctx context.Context, p AISettingsRequest) AISettingsResponse {
	r := AISettingsResponse{Providers: []AIProviderOption{}}
	if !p.Valid() {
		r.Code = "validation"
		return r
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || (p.AccountID != "" && p.AccountID != string(account)) {
		r.Code = "unauthorized"
		return r
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		r.Code = "cancelled"
		return r
	}
	cfg, err := s.settings.Settings(ctx, account)
	if err != nil {
		r.Code = aiWorkflowCode(err)
		return r
	}
	if p.Operation != "status" {
		s.clearLocked() // Provider/model/key changes revoke every pending approval.
		if p.Operation == "disable" {
			cfg.Enabled = false
			err = s.settings.SaveSettings(ctx, account, cfg)
			if adapter, ok := s.providers[cfg.Provider]; err == nil && ok {
				err = adapter.Keys(account).Delete(ctx)
			}
		} else {
			adapter, ok := s.providers[p.Provider]
			if !ok {
				r.Code = "unsupported"
				return r
			}
			_, err = adapter.New(account, p.Model) // Constructor validates configuration; no network.
			if err == nil {
				if !p.APIKey.Empty() {
					err = adapter.Keys(account).Save(ctx, p.APIKey.Secret)
				} else {
					var key credentials.Secret
					key, err = adapter.Keys(account).Load(ctx)
					if err == nil && key.Empty() {
						err = ai.ErrCredentials
					}
				}
			}
			if err == nil {
				cfg = ai.Settings{Provider: p.Provider, Model: p.Model, Enabled: true}
				err = s.settings.SaveSettings(ctx, account, cfg)
			}
		}
		if err != nil {
			r.Code = aiWorkflowCode(err)
			return r
		}
	}
	if session.Err() != nil || ctx.Err() != nil {
		r.Code = "unauthorized"
		return r
	}
	r.AccountID = string(account)
	r.Provider = cfg.Provider
	r.Model = cfg.Model
	r.Enabled = cfg.Enabled
	for id, adapter := range s.providers {
		r.Providers = append(r.Providers, AIProviderOption{ID: id, External: adapter.External})
	}
	sort.Slice(r.Providers, func(i, j int) bool { return r.Providers[i].ID < r.Providers[j].ID })
	return r
}
func clipSummary(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
func summaryPayload(row repository.StoredEmail) (ai.SummaryRequest, bool, error) {
	e := row.Email
	if e.BodyState != "complete" {
		return ai.SummaryRequest{}, false, repository.ErrInvalid
	}
	body, err := mailtext.CleanBody(e)
	if err != nil || strings.TrimSpace(body) == "" {
		return ai.SummaryRequest{}, false, repository.ErrInvalid
	}
	clean := func(value string) string { return strings.Join(strings.Fields(value), " ") }
	text := "Subject: " + clipSummary(clean(e.Subject), 512) + "\nSender: " + clipSummary(clean(e.Sender.Name), 256) + " <" + clipSummary(clean(e.Sender.Address), 320) + ">\nReceived: " + e.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z") + "\nBody:\n" + body
	truncated := len(text) > ai.MaxInputBytes
	request := ai.SummaryRequest{EmailID: e.ID, Revision: row.ContentHash, Email: ai.NewText(clipSummary(text, ai.MaxInputBytes))}
	return request, truncated, request.Validate()
}
func (s *AIService) fresh(ctx context.Context, account domain.AccountID, source ai.SummaryRequest, identity ai.Identity) error {
	cfg, err := s.settings.Settings(ctx, account)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.Provider != identity.Provider || cfg.Model != identity.Model {
		return ai.ErrConsent
	}
	adapter, ok := s.providers[cfg.Provider]
	if !ok {
		return ai.ErrConsent
	}
	key, err := adapter.Keys(account).Load(ctx)
	if err != nil || key.Empty() {
		return ai.ErrCredentials
	}
	if !source.MatchesKey(key) {
		return ai.ErrConsent
	}
	row, err := s.emails.Get(ctx, account, source.EmailID)
	if err != nil {
		return err
	}
	if row.Email.AccountID != account || row.Email.ID != source.EmailID || row.ContentHash != source.Revision {
		return repository.ErrConflict
	}
	return ctx.Err()
}
func (s *AIService) Summarize(ctx context.Context, p SummarizeEmailRequest) SummarizeEmailResponse {
	fail := func(code string) SummarizeEmailResponse { r := emptySummary(); r.Code = code; return r }
	if !p.Valid() {
		return fail("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil {
		s.Clear()
		return fail("unauthorized")
	}
	if p.AccountID != string(account) {
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
	if p.Operation == "clear" {
		if s.response.EmailID == p.EmailID {
			s.clearLocked()
		}
		return emptySummary()
	}
	if p.Operation == "cancel" {
		if !s.busy || s.response.JobID != p.JobID || s.response.EmailID != p.EmailID {
			return fail("stale_preview")
		}
		s.clearLocked()
		return fail("cancelled")
	}
	if p.Operation == "status" {
		if s.response.EmailID != p.EmailID {
			return emptySummary()
		}
		if s.snapshot.EmailID != "" {
			if err = s.fresh(ctx, account, s.snapshot, ai.Identity{Provider: s.response.Provider, Model: s.response.Model}); err != nil {
				s.clearLocked()
				return fail(aiWorkflowCode(err))
			}
		}
		return s.response
	}
	if s.busy {
		return fail("busy")
	}
	cfg, err := s.settings.Settings(ctx, account)
	if err != nil {
		return fail(aiWorkflowCode(err))
	}
	if !cfg.Enabled {
		s.clearLocked()
		return fail("ai_disabled")
	}
	adapter, ok := s.providers[cfg.Provider]
	if !ok {
		return fail("unsupported")
	}
	if p.Operation == "preview" {
		s.clearLocked()
		row, err := s.emails.Get(ctx, account, domain.EmailID(p.EmailID))
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		if row.Email.AccountID != account || row.Email.ID != domain.EmailID(p.EmailID) {
			return fail("unauthorized")
		}
		payload, truncated, err := summaryPayload(row)
		if err != nil {
			return fail("context_unavailable")
		}
		key, err := adapter.Keys(account).Load(ctx)
		if err != nil || key.Empty() {
			return fail("ai_credentials_unavailable")
		}
		payload.KeyFingerprint = ai.KeyFingerprint(key)
		engine, err := adapter.New(account, cfg.Model)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		identity := ai.Identity{Provider: cfg.Provider, Model: cfg.Model, External: adapter.External}
		consent, err := ai.NewService(engine, identity)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		preview, err := consent.Prepare(ctx, account, payload)
		if err != nil {
			return fail(aiWorkflowCode(err))
		}
		if session.Err() != nil || ctx.Err() != nil {
			consent.Clear()
			return fail("unauthorized")
		}
		s.consent = consent
		s.snapshot = payload
		s.response = SummarizeEmailResponse{AccountID: string(account), EmailID: p.EmailID, State: "preview", PreviewID: preview.ID, ExpiresAt: preview.ExpiresAt.UnixMilli(), Provider: identity.Provider, Model: identity.Model, External: identity.External, Email: payload.Email.Reveal(), Policy: preview.Instructions.Reveal(), Truncated: truncated}
		return s.response
	}
	if s.consent == nil || s.response.State != "preview" || s.response.PreviewID != p.PreviewID || s.response.EmailID != p.EmailID || s.response.Provider != cfg.Provider || s.response.Model != cfg.Model {
		return fail("stale_preview")
	}
	identity := ai.Identity{Provider: cfg.Provider, Model: cfg.Model}
	if err = s.fresh(ctx, account, s.snapshot, identity); err != nil {
		s.clearLocked()
		return fail(aiWorkflowCode(err))
	}
	consent, source := s.consent, s.snapshot
	job, jobCancel := context.WithTimeout(s.root, 75*time.Second)
	stopJob := context.AfterFunc(session, jobCancel)
	s.cancel = jobCancel
	s.busy = true
	s.response.State = "running"
	s.response.JobID = rand.Text()
	s.response.Email = ""
	s.response.Policy = ""
	s.response.PreviewID = ""
	s.response.ExpiresAt = 0
	generation := s.generation
	base := s.response
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer jobCancel()
		defer stopJob()
		var result ai.SummaryResult
		err := s.fresh(job, account, source, identity)
		if err == nil {
			result, err = consent.Execute(job, account, p.PreviewID, p.Approved)
		}
		if err == nil {
			err = s.fresh(job, account, source, identity)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.cancel = nil
		if s.closed || generation != s.generation || session.Err() != nil {
			s.response = emptySummary()
			return
		}
		base.JobID = ""
		base.State = "completed"
		if err != nil {
			base.State = "error"
			base.Code = aiWorkflowCode(err)
		} else {
			dto := &SummaryResultDTO{Summary: result.Summary.Reveal(), Requests: []string{}, Deadlines: []string{}}
			for _, v := range result.Requests {
				dto.Requests = append(dto.Requests, v.Reveal())
			}
			for _, v := range result.Deadlines {
				dto.Deadlines = append(dto.Deadlines, v.Reveal())
			}
			base.Result = dto
		}
		s.response = boundSummary(base)
	}()
	return s.response
}
