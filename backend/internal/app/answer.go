package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

type answerRetriever interface {
	SearchMode(context.Context, domain.AccountID, string, int, repository.SearchFilter, string) (retrieval.Result, error)
	CheckSources(context.Context, domain.AccountID, repository.SearchFilter, []repository.VectorSource) error
}
type questionState struct {
	account  domain.AccountID
	session  context.Context
	settings ai.Settings
	engine   ai.AIProvider
	request  ai.AnswerRequest
	stamps   []repository.VectorSource
	filter   repository.SearchFilter
	expires  time.Time
	response AnswerResponse
	jobID    string
}

func (a *App) AnswerEmail(ctx context.Context, r AnswerRequest) AnswerResponse {
	if a.AI == nil {
		return emptyAnswer("unauthorized")
	}
	return a.AI.Answer(ctx, r)
}

func (s *AIService) questionFresh(ctx context.Context, q *questionState) error {
	account, session, err := s.session()
	if err != nil || account != q.account || session != q.session || session == nil || session.Err() != nil {
		return ai.ErrConsent
	}
	if !s.now().Before(q.expires) {
		return ai.ErrConsent
	}
	cfg, err := s.settings.Settings(ctx, q.account)
	if err != nil {
		return err
	}
	if cfg != q.settings || !cfg.Enabled {
		return ai.ErrConsent
	}
	adapter, ok := s.providers[cfg.Provider]
	if !ok {
		return ai.ErrConsent
	}
	key, err := adapter.Keys(q.account).Load(ctx)
	if err != nil {
		return err
	}
	if !q.request.MatchesKey(key) {
		return ai.ErrConsent
	}
	for _, v := range q.stamps {
		if v.Chunk.AccountID != q.account {
			return repository.ErrConflict
		}
	}
	return s.retriever.CheckSources(ctx, q.account, q.filter, selectedStamps(q))
}

// prepareQuestion binds an immutable payload, source order, policy, destination,
// key identity and session to a new one-use token. Selection always revokes it.
func (s *AIService) prepareQuestion(q *questionState, ids []string) error {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	sources := []ai.Evidence{}
	for _, e := range q.response.Evidence {
		if selected[e.SourceID] {
			sources = append(sources, e)
		}
	}
	if len(sources) != len(selected) {
		return ai.ErrInvalid
	}
	q.request.Sources = sources
	if err := q.request.Validate(); err != nil {
		return err
	}
	input, err := q.request.Input()
	if err != nil {
		return err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return ai.ErrUnavailable
	}
	q.expires = s.now().Add(5 * time.Minute)
	q.response.State = "preview"
	q.response.JobID = ""
	q.response.PreviewID = hex.EncodeToString(nonce[:])
	q.response.ExpiresAt = q.expires.UnixMilli()
	q.response.Policy = ai.AnswerInstructions()
	q.response.Payload = input.Reveal()
	q.response.SelectedIDs = []string{}
	for _, source := range sources {
		q.response.SelectedIDs = append(q.response.SelectedIDs, source.SourceID)
	}
	// The digest is not an authority by itself; the server keeps the session and
	// exact source fences, and accepts only its current pending random token.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%x\x00%d\x00%s\x00%s", q.response.PreviewID, q.account, q.settings.Provider, q.settings.Model, q.request.KeyFingerprint, q.response.ExpiresAt, q.response.Policy, q.response.Payload)))
	q.response.Digest = hex.EncodeToString(digest[:])
	return nil
}

func answerEvidence(account domain.AccountID, result retrieval.Result) ([]ai.Evidence, error) {
	stamps := map[string]repository.VectorSource{}
	for _, v := range result.Sources {
		if v.Chunk.AccountID != account {
			return nil, repository.ErrConflict
		}
		stamps[v.Chunk.ID] = v
	}
	evidence := []ai.Evidence{}
	seen := map[string]bool{}
	// Take the best passage from each ranked email before additional passages.
	for ordinal := 0; ordinal < retrieval.MaxEvidence; ordinal++ {
		for _, email := range result.Emails {
			if ordinal >= len(email.Evidence) {
				continue
			}
			ev := email.Evidence[ordinal]
			v, ok := stamps[ev.ChunkID]
			if !ok || string(v.Chunk.EmailID) != email.EmailID || v.Chunk.SourceRevision != ev.SourceRevision || mailtext.ReaderRevision(ev.SourceRevision) != email.ReaderRevision {
				return nil, repository.ErrConflict
			}
			if seen[ev.ChunkID] {
				continue
			}
			seen[ev.ChunkID] = true
			evidence = append(evidence, ai.Evidence{SourceID: fmt.Sprintf("S%d", len(evidence)+1), EmailID: email.EmailID, ChunkID: ev.ChunkID, SourceRevision: ev.SourceRevision, ReaderRevision: email.ReaderRevision, Subject: clipSummary(email.Subject, 120), Sender: clipSummary(email.Sender, 120), ReceivedAt: email.ReceivedAt, Excerpt: ev.Excerpt, PartialContent: email.PartialContent})
			if len(evidence) == ai.MaxAnswerSources {
				return evidence, nil
			}
		}
	}
	return evidence, nil
}

func selectedStamps(q *questionState) []repository.VectorSource {
	ids := map[string]bool{}
	for _, e := range q.request.Sources {
		ids[e.ChunkID] = true
	}
	stamps := []repository.VectorSource{}
	for _, v := range q.stamps {
		if ids[v.Chunk.ID] {
			stamps = append(stamps, v)
		}
	}
	return stamps
}

// Answer shares AIService's one transient worker and settings/key authority with
// Summary. Retrieval is local; only approve can call the selected AI provider.
func (s *AIService) Answer(ctx context.Context, r AnswerRequest) AnswerResponse {
	if !r.Valid() {
		return emptyAnswer("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || r.AccountID != "" && r.AccountID != string(account) {
		s.Clear()
		return emptyAnswer("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return emptyAnswer("cancelled")
	}
	if s.retriever == nil {
		return emptyAnswer("unsupported")
	}
	if q := s.answer; q != nil && (q.account != account || q.session != session) {
		s.clearLocked()
	}
	base := emptyAnswer("")
	base.AccountID = string(account)
	q := s.answer
	if r.Operation == "clear" {
		if q != nil {
			s.clearLocked()
		}
		return base
	}
	if r.Operation == "cancel" {
		if q == nil || q.jobID != r.JobID || !s.busy {
			return emptyAnswer("stale_preview")
		}
		s.clearLocked()
		return emptyAnswer("cancelled")
	}
	if r.Operation == "status" {
		if q == nil {
			return base
		}
		if r.JobID != "" && q.jobID != r.JobID {
			return emptyAnswer("stale_preview")
		}
		if q.response.State == "preview" || q.response.State == "running" || q.response.State == "completed" {
			if err = s.questionFresh(ctx, q); err != nil {
				s.clearLocked()
				return emptyAnswer(aiWorkflowCode(err))
			}
		}
		return boundAnswer(q.response)
	}
	if s.busy {
		return emptyAnswer("busy")
	}
	if r.Operation == "retrieve" {
		s.clearLocked()
		cfg, err := s.settings.Settings(ctx, account)
		if err != nil {
			return emptyAnswer(aiWorkflowCode(err))
		}
		if !cfg.Enabled {
			return emptyAnswer("ai_disabled")
		}
		adapter, ok := s.providers[cfg.Provider]
		if !ok {
			return emptyAnswer("unsupported")
		}
		key, err := adapter.Keys(account).Load(ctx)
		if err != nil || key.Empty() {
			return emptyAnswer("ai_credentials_unavailable")
		}
		engine, err := adapter.New(account, cfg.Model)
		if err != nil {
			return emptyAnswer(aiWorkflowCode(err))
		}
		var jobID [16]byte
		if _, err = rand.Read(jobID[:]); err != nil {
			return emptyAnswer("operation_failed")
		}
		q = &questionState{account: account, session: session, settings: cfg, engine: engine, filter: r.Filter, request: ai.AnswerRequest{Question: ai.NewText(r.Question), KeyFingerprint: ai.KeyFingerprint(key)}, response: base}
		q.response.State = "retrieving"
		q.response.JobID = hex.EncodeToString(jobID[:])
		q.response.Question = r.Question
		q.response.Provider = cfg.Provider
		q.response.Model = cfg.Model
		q.response.External = adapter.External
		q.response.Aggregate = ai.AggregateQuestion(r.Question)
		q.jobID = q.response.JobID
		s.answer = q
		job, jobCancel := context.WithTimeout(s.root, 30*time.Second)
		revoke := context.AfterFunc(session, jobCancel)
		s.cancel = jobCancel
		s.busy = true
		generation := s.generation
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer jobCancel()
			defer revoke()
			result, err := s.retriever.SearchMode(job, account, r.Question, 5, r.Filter, "hybrid")
			var evidence []ai.Evidence
			coverage := AnswerCoverage{}
			if err == nil {
				evidence, err = answerEvidence(account, result)
			}
			if err == nil {
				var progress repository.IndexProgress
				progress, err = s.answerIndex.Progress(job, account, s.answerProfile)
				coverage.Eligible, coverage.Semantic = progress.Eligible, progress.Complete
				if err == nil && s.answerWords != nil {
					coverage.Keyword, err = s.answerWords.KeywordCoverage(job, account, s.answerProfile)
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.busy = false
			s.cancel = nil
			if s.closed || generation != s.generation || s.answer != q || session.Err() != nil {
				return
			}
			q.response.JobID = ""
			if err != nil {
				q.response = emptyAnswer(searchError(err))
				q.response.State = "error"
				return
			}
			q.response.Evidence = evidence
			q.response.Mode = result.Mode
			q.response.Warning = result.Warning
			q.response.Coverage = coverage
			if len(evidence) == 0 {
				q.response.State = "completed"
				q.response.Result = &ai.AnswerResult{Outcome: "insufficient", Statements: []ai.AnswerStatement{}, Limitations: []string{"No matching cached evidence was retrieved.", ai.AnswerScope}}
				q.expires = s.now().Add(5 * time.Minute)
				// No evidence means no generation; status still rechecks settings/key/session.
				q.request.Sources = nil
				return
			}
			q.stamps = result.Sources
			ids := []string{}
			for _, e := range evidence {
				ids = append(ids, e.SourceID)
			}
			if err = s.prepareQuestion(q, ids); err == nil {
				q.stamps = selectedStamps(q)
				err = s.questionFresh(job, q)
			}
			if err != nil {
				q.response = emptyAnswer(aiWorkflowCode(err))
				q.response.State = "error"
				return
			}
			if bound := boundAnswer(q.response); bound.Code != "" {
				q.response = bound
			}
		}()
		return boundAnswer(q.response)
	}
	if q == nil || q.response.State != "preview" || q.response.PreviewID != r.PreviewID {
		return emptyAnswer("stale_preview")
	}
	if err = s.questionFresh(ctx, q); err != nil {
		s.clearLocked()
		return emptyAnswer(aiWorkflowCode(err))
	}
	if r.Operation == "select" {
		err = s.prepareQuestion(q, r.SourceIDs)
		if err == nil {
			err = s.questionFresh(ctx, q)
		}
		if err != nil {
			s.clearLocked()
			return emptyAnswer(aiWorkflowCode(err))
		}
		return boundAnswer(q.response)
	}
	if r.Digest != q.response.Digest {
		return emptyAnswer("stale_preview")
	}
	// Top-K retrieval never proves an aggregate or a mailbox-wide absence. Refuse
	// these common requests locally without spending an approval/provider call.
	if q.response.Aggregate {
		return emptyAnswer("non_exhaustive")
	}
	q.stamps = selectedStamps(q)
	q.response.State = "running"
	q.response.PreviewID = ""
	q.response.Digest = ""
	q.response.Payload = ""
	q.response.Policy = ""
	q.response.ExpiresAt = 0
	q.response.Evidence = append([]ai.Evidence{}, q.request.Sources...)
	var jobID [16]byte
	if _, err = rand.Read(jobID[:]); err != nil {
		s.clearLocked()
		return emptyAnswer("operation_failed")
	}
	q.response.JobID = hex.EncodeToString(jobID[:])
	q.jobID = q.response.JobID
	job, jobCancel := context.WithTimeout(s.root, 75*time.Second)
	revoke := context.AfterFunc(session, jobCancel)
	s.cancel = jobCancel
	s.busy = true
	generation := s.generation
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer jobCancel()
		defer revoke()
		err := s.questionFresh(job, q)
		var result ai.AnswerResult
		if err == nil {
			result, err = q.engine.AnswerWithEvidence(job, q.request)
			if err != nil {
				err = ai.SafeError(err)
			}
		}
		if err == nil {
			err = result.Validate(q.request)
		}
		if err == nil {
			err = s.questionFresh(job, q)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.cancel = nil
		if s.closed || generation != s.generation || s.answer != q || session.Err() != nil {
			return
		}
		q.response.JobID = ""
		q.response.State = "completed"
		if err != nil {
			q.response = emptyAnswer(aiWorkflowCode(err))
			q.response.State = "error"
			return
		}
		// Scope is imposed by Go even if a provider omits its own limitations.
		result.Limitations = append(result.Limitations, ai.AnswerScope)
		q.response.Result = &result
		q.response = boundAnswer(q.response)
	}()
	return boundAnswer(q.response)
}
