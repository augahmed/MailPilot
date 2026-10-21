package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

type SearchRequest struct {
	Operation string                  `json:"operation"`
	AccountID string                  `json:"accountId"`
	JobID     string                  `json:"jobId"`
	Query     string                  `json:"query"`
	Limit     int                     `json:"limit"`
	Filter    repository.SearchFilter `json:"filter"`
	Mode      string                  `json:"mode,omitempty"`
}

func (r SearchRequest) Valid() bool {
	if len(r.AccountID) > 100 || len(r.JobID) > 32 {
		return false
	}
	switch r.Operation {
	case "start":
		return r.AccountID != "" && r.JobID == "" && retrieval.ValidMode(r.Mode) && retrieval.ValidQuery(r.Query, r.Limit, r.Filter)
	case "status", "cancel":
		return r.Mode == "" && r.Query == "" && r.Limit == 0 && r.Filter == (repository.SearchFilter{}) && (r.Operation == "status" || r.AccountID != "" && r.JobID != "") && (r.JobID == "" || len(r.JobID) == 32)
	default:
		return false
	}
}

type SearchResponse struct {
	AccountID          string                  `json:"accountId"`
	JobID              string                  `json:"jobId"`
	Status             string                  `json:"status"`
	Code               string                  `json:"code"`
	Emails             []retrieval.EmailResult `json:"emails"`
	ScannedChunks      int                     `json:"scannedChunks"`
	ElapsedMS          float64                 `json:"elapsedMs"`
	SearchableMessages int                     `json:"searchableMessages"`
	EligibleMessages   int                     `json:"eligibleMessages"`
	KeywordMessages    int                     `json:"keywordMessages"`
	Mode               string                  `json:"mode"`
	RequestedMode      string                  `json:"requestedMode"`
	Warning            string                  `json:"warning"`
}
type searchJob struct {
	id       string
	account  domain.AccountID
	session  context.Context
	filter   repository.SearchFilter
	cancel   context.CancelFunc
	done     chan struct{}
	expires  time.Time
	result   retrieval.Result
	response SearchResponse
}

// LocalSearch owns only one short-lived local query. It shares the indexing
// provider; it never starts/rebuilds an index or calls an AI/Gmail adapter.
type LocalSearch struct {
	root     context.Context
	session  func() (domain.AccountID, context.Context, error)
	search   *retrieval.VectorSearch
	coverage repository.IndexRepository
	mu       sync.Mutex
	job      *searchJob
	closed   bool
}

func NewLocalSearch(root context.Context, session func() (domain.AccountID, context.Context, error), search *retrieval.VectorSearch, coverage repository.IndexRepository) *LocalSearch {
	return &LocalSearch{root: root, session: session, search: search, coverage: coverage}
}
func (a *App) SearchLocal(ctx context.Context, r SearchRequest) SearchResponse {
	if a.Search == nil {
		return emptySearch("unauthorized")
	}
	return a.Search.Manage(ctx, r)
}
func emptySearch(code string) SearchResponse {
	return SearchResponse{Status: "idle", Code: code, Emails: []retrieval.EmailResult{}}
}
func (s *LocalSearch) Manage(ctx context.Context, r SearchRequest) SearchResponse {
	if !r.Valid() {
		return emptySearch("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || r.AccountID != "" && r.AccountID != string(account) {
		s.Clear()
		return emptySearch("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return emptySearch("unavailable")
	}
	if s.job != nil && (s.job.account != account || s.job.session != session || time.Now().After(s.job.expires)) {
		s.job.cancel()
		s.job.result = retrieval.Result{}
		s.job.response = emptySearch("cancelled")
		s.job.response.Status = "cancelled"
		select {
		case <-s.job.done:
			s.job = nil
		default:
			response := emptySearch("busy")
			response.AccountID = string(account)
			return response
		}
	}
	if r.Operation == "start" {
		if s.job != nil {
			select {
			case <-s.job.done:
			default:
				return emptySearch("busy")
			}
		}
		var bytes [16]byte
		if _, err = rand.Read(bytes[:]); err != nil {
			return emptySearch("unavailable")
		}
		jobCtx, jobCancel := context.WithTimeout(s.root, 30*time.Second)
		j := &searchJob{id: hex.EncodeToString(bytes[:]), account: account, session: session, filter: r.Filter, cancel: jobCancel, done: make(chan struct{}), expires: time.Now().Add(5 * time.Minute)}
		j.response = emptySearch("")
		j.response.AccountID = string(account)
		j.response.JobID = j.id
		j.response.Status = "running"
		j.response.RequestedMode = r.Mode
		if j.response.RequestedMode == "" {
			j.response.RequestedMode = "semantic"
		}
		s.job = j
		revoke := context.AfterFunc(session, jobCancel)
		go func() {
			defer close(j.done)
			defer jobCancel()
			defer revoke()
			result, err := s.search.SearchMode(jobCtx, account, r.Query, r.Limit, r.Filter, r.Mode)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.job != j || session.Err() != nil {
				return
			}
			if j.response.Status == "cancelled" {
				return
			}
			if err != nil {
				j.response.Status = "error"
				j.response.Code = searchError(err)
				return
			}
			j.result = result
			j.response.Status = "complete"
			j.response.Emails = result.Emails
			j.response.ScannedChunks = result.Scanned
			j.response.ElapsedMS = float64(result.Elapsed.Microseconds()) / 1000
			j.response.Mode = result.Mode
			j.response.Warning = result.Warning
		}()
	}
	result := emptySearch("")
	result.AccountID = string(account)
	if j := s.job; j != nil {
		if r.JobID != "" && r.JobID != j.id {
			return emptySearch("stale")
		}
		if r.Operation == "cancel" {
			j.cancel()
			j.result = retrieval.Result{}
			j.response.Emails = []retrieval.EmailResult{}
			j.response.Status = "cancelled"
			j.response.Code = "cancelled"
		}
		if j.response.Status == "complete" {
			if err = s.search.CheckSources(ctx, account, j.filter, j.result.Sources); err != nil {
				j.result = retrieval.Result{}
				j.response.Emails = []retrieval.EmailResult{}
				j.response.Status = "error"
				j.response.Code = searchError(err)
			}
		}
		result = j.response
	} else if r.JobID != "" {
		return emptySearch("stale")
	}
	coverage, err := s.coverage.Progress(ctx, account, s.search.Profile)
	if err != nil {
		return emptySearch(searchError(err))
	}
	result.SearchableMessages = coverage.Complete
	result.EligibleMessages = coverage.Eligible
	if s.search.Keywords != nil {
		result.KeywordMessages, err = s.search.Keywords.KeywordCoverage(ctx, account, s.search.Profile)
		if err != nil {
			return emptySearch(searchError(err))
		}
	}
	if session.Err() != nil {
		return emptySearch("unauthorized")
	}
	// The transport is capped after JSON escaping. Reject oversize metadata/output
	// as a whole rather than silently losing best evidence or emitting broken JSON.
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 48*1024 {
		if s.job != nil {
			keywordMessages := result.KeywordMessages
			s.job.result = retrieval.Result{}
			s.job.response = emptySearch("too_large")
			s.job.response.AccountID = string(account)
			s.job.response.JobID = s.job.id
			s.job.response.Status = "error"
			result = s.job.response
			result.SearchableMessages = coverage.Complete
			result.EligibleMessages = coverage.Eligible
			result.KeywordMessages = keywordMessages
			return result
		}
		return emptySearch("too_large")
	}
	return result
}
func searchError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, retrieval.ErrInput), errors.Is(err, embedding.ErrInput):
		return "validation"
	case errors.Is(err, embedding.ErrUnavailable), errors.Is(err, embedding.ErrClosed):
		return "model_unavailable"
	case errors.Is(err, embedding.ErrInference), errors.Is(err, embedding.ErrVector):
		return "inference_failed"
	case errors.Is(err, repository.ErrConflict), errors.Is(err, repository.ErrNotFound):
		return "stale"
	default:
		return "storage_error"
	}
}
func (s *LocalSearch) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil {
		s.job.cancel()
		s.job.result = retrieval.Result{}
		s.job.response = emptySearch("cancelled")
		s.job.response.Status = "cancelled"
	}
}
func (s *LocalSearch) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	j := s.job
	if j != nil {
		j.cancel()
	}
	s.mu.Unlock()
	if j != nil {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.Clear()
	return nil
}
