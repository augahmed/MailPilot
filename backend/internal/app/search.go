package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/search"
)

type SearchRequest struct {
	Operation  string `json:"operation"`
	AccountID  string `json:"accountId"`
	JobID      string `json:"jobId"`
	Query      string `json:"query"`
	TopK       int    `json:"topK"`
	Sender     string `json:"sender"`
	CategoryID string `json:"categoryId"`
	From       *int64 `json:"from"`
	Until      *int64 `json:"until"`
}

func (p SearchRequest) filters() search.Filters {
	return search.Filters{From: p.From, Until: p.Until, Sender: p.Sender, CategoryID: domain.CategoryID(p.CategoryID)}
}
func (p SearchRequest) Valid() bool {
	if len(p.AccountID) > 100 || len(p.JobID) > 100 || p.filters().Validate() != nil || !utf8.ValidString(p.Query) || len(p.Query) > 1000 {
		return false
	}
	empty := p.Query == "" && p.TopK == 0 && p.Sender == "" && p.CategoryID == "" && p.From == nil && p.Until == nil
	switch p.Operation {
	case "status":
		return empty
	case "cancel":
		return empty && p.AccountID != "" && p.JobID != ""
	case "index":
		return empty && p.AccountID != "" && p.JobID == ""
	case "query":
		return p.AccountID != "" && p.JobID == "" && strings.TrimSpace(p.Query) != "" && p.TopK >= 1 && p.TopK <= search.MaxTopK
	default:
		return false
	}
}

type SearchHitDTO struct {
	EmailID    string  `json:"emailId"`
	Sender     string  `json:"sender"`
	Subject    string  `json:"subject"`
	ReceivedAt int64   `json:"receivedAt"`
	Similarity float64 `json:"similarity"`
	Excerpt    string  `json:"excerpt"`
}
type SearchResponse struct {
	AccountID       string         `json:"accountId"`
	JobID           string         `json:"jobId"`
	Operation       string         `json:"operation"`
	State           string         `json:"state"`
	Counts          search.Counts  `json:"counts"`
	Processed       int            `json:"processed"`
	Errors          int            `json:"errors"`
	Scanned         int            `json:"scanned"`
	Partial         bool           `json:"partial"`
	Capped          bool           `json:"capped"`
	ExcerptFallback bool           `json:"excerptFallback"`
	Rows            []SearchHitDTO `json:"rows"`
	Code            string         `json:"code,omitempty"`
}

func emptySearch() SearchResponse { return SearchResponse{State: "idle", Rows: []SearchHitDTO{}} }

type MailboxSearch struct {
	mu           sync.Mutex
	root         context.Context
	session      func() (domain.AccountID, context.Context, error)
	service      *search.Service
	categories   repository.CategoryRepository
	response     SearchResponse
	cancel       context.CancelFunc
	busy, closed bool
	generation   uint64
	wg           sync.WaitGroup
}

func NewMailboxSearch(root context.Context, session func() (domain.AccountID, context.Context, error), service *search.Service, categories repository.CategoryRepository) *MailboxSearch {
	return &MailboxSearch{root: root, session: session, service: service, categories: categories, response: emptySearch()}
}
func (s *MailboxSearch) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.generation++
	s.response = emptySearch()
}
func (s *MailboxSearch) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.generation++
	s.response = emptySearch()
	s.mu.Unlock()
	s.wg.Wait()
}
func (a *App) SearchMailbox(ctx context.Context, p SearchRequest) SearchResponse {
	if a.Search == nil {
		r := emptySearch()
		r.Code = "unauthorized"
		return r
	}
	return a.Search.Handle(ctx, p)
}
func searchCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(err, repository.ErrConflict):
		return "conflict"
	case errors.Is(err, search.ErrInput), errors.Is(err, embedding.ErrInput), errors.Is(err, repository.ErrInvalid):
		return "validation"
	case errors.Is(err, embedding.ErrProvider), errors.Is(err, embedding.ErrUnavailable), errors.Is(err, embedding.ErrVector):
		return "embedding_unavailable"
	default:
		return "storage_error"
	}
}
func (s *MailboxSearch) Handle(ctx context.Context, p SearchRequest) SearchResponse {
	fail := func(code string) SearchResponse { r := emptySearch(); r.Code = code; return r }
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
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	if p.CategoryID != "" {
		if _, err := s.categories.Get(call, account, domain.CategoryID(p.CategoryID)); err != nil {
			return fail("validation")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fail("cancelled")
	}
	if s.response.AccountID != string(account) {
		s.response = emptySearch()
		s.response.AccountID = string(account)
	}
	if p.JobID != "" && p.JobID != s.response.JobID {
		return fail("conflict")
	}
	if p.Operation == "cancel" {
		if s.cancel != nil {
			s.cancel()
		}
		return s.response
	}
	counts, err := s.service.Index.Counts(call, account, s.service.Model)
	if err != nil {
		return fail(searchCode(err))
	}
	s.response.Counts = counts
	if session.Err() != nil {
		return fail("unauthorized")
	}
	if p.Operation == "status" {
		if s.service.Embeddings == nil {
			s.response.Code = "embedding_unavailable"
		}
		return s.response
	}
	if s.busy {
		r := s.response
		r.Code = "busy"
		return r
	}
	if s.service.Embeddings == nil {
		r := s.response
		r.Code = "embedding_unavailable"
		return r
	}
	timeout := time.Minute
	if p.Operation == "index" {
		timeout = 10 * time.Minute
	}
	worker, cancelWorker := context.WithTimeout(s.root, timeout)
	s.cancel = cancelWorker
	stopSession := context.AfterFunc(session, cancelWorker)
	s.generation++
	g := s.generation
	s.busy = true
	s.response = emptySearch()
	s.response.AccountID = string(account)
	s.response.JobID = rand.Text()
	s.response.Operation = p.Operation
	s.response.Counts = counts
	s.response.State = "searching"
	if p.Operation == "index" {
		s.response.State = "indexing"
	}
	initial := s.response
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancelWorker()
		defer stopSession()
		var result SearchResponse
		var err error
		if p.Operation == "index" {
			result, err = s.index(worker, account, g, initial)
		} else {
			result, err = s.query(worker, account, p, initial)
		}
		if latest, countErr := s.service.Index.Counts(worker, account, s.service.Model); countErr == nil {
			result.Counts = latest
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		if g != s.generation {
			return
		}
		if session.Err() != nil {
			s.response = emptySearch()
			return
		}
		result.State = "completed"
		if err != nil {
			result.Code = searchCode(err)
			result.State = "error"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				result.State = "cancelled"
			}
			result.Partial = true
			result.Rows = []SearchHitDTO{}
		}
		s.response = boundedSearch(result)
	}()
	return s.response
}
func (s *MailboxSearch) update(g uint64, r SearchResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g == s.generation {
		s.response = r
	}
}
func (s *MailboxSearch) index(ctx context.Context, a domain.AccountID, g uint64, r SearchResponse) (SearchResponse, error) {
	after := domain.EmailID("")
	for r.Processed+r.Errors < search.MaxScan {
		ids, err := s.service.Index.Missing(ctx, a, s.service.Model, after, min(embedding.MaxBatchSize, search.MaxScan-r.Processed-r.Errors))
		if err != nil {
			return r, err
		}
		if len(ids) == 0 {
			return r, nil
		}
		if ids[len(ids)-1] <= after {
			return r, repository.ErrStorage
		}
		after = ids[len(ids)-1]
		_, err = s.service.Embeddings.EmbedEmails(ctx, a, ids)
		if err == nil {
			r.Processed += len(ids)
		} else if errors.Is(err, embedding.ErrInput) || errors.Is(err, embedding.ErrUnavailable) || errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrNotFound) {
			// A malformed/stale source must not prevent other cached messages indexing.
			for _, id := range ids {
				if ctx.Err() != nil {
					return r, ctx.Err()
				}
				if _, oneErr := s.service.Embeddings.EmbedEmails(ctx, a, []domain.EmailID{id}); oneErr != nil {
					r.Errors++
					r.Code = searchCode(oneErr)
				} else {
					r.Processed++
				}
			}
		} else {
			r.Errors += len(ids)
			return r, err
		}
		r.Partial = r.Errors > 0
		if counts, err := s.service.Index.Counts(ctx, a, s.service.Model); err == nil {
			r.Counts = counts
		} else {
			return r, err
		}
		s.update(g, r)
	}
	r.Capped = true
	r.Partial = true
	return r, nil
}
func (s *MailboxSearch) query(ctx context.Context, a domain.AccountID, p SearchRequest, r SearchResponse) (SearchResponse, error) {
	result, err := s.service.Query(ctx, a, p.Query, p.TopK, p.filters())
	r.Scanned = result.Scanned
	r.Errors = result.Errors
	r.Partial = result.Partial
	r.Capped = result.Scanned >= search.MaxScan && result.Partial
	r.ExcerptFallback = result.ExcerptFallback
	if err != nil {
		return r, err
	}
	for _, hit := range result.Hits {
		sender := hit.Stored.Email.Sender.Address
		if hit.Stored.Email.Sender.Name != "" {
			sender = hit.Stored.Email.Sender.Name + " <" + sender + ">"
		}
		r.Rows = append(r.Rows, SearchHitDTO{EmailID: string(hit.Stored.Email.ID), Sender: searchDisplay(sender, 80), Subject: searchDisplay(hit.Stored.Email.Subject, 120), ReceivedAt: hit.Stored.Email.ReceivedAt.UnixMilli(), Similarity: hit.Similarity, Excerpt: searchDisplay(hit.Excerpt, 160)})
	}
	return r, nil
}
func searchDisplay(text string, n int) string {
	text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	return shortText(text, n)
}
func boundedSearch(r SearchResponse) SearchResponse {
	data, err := json.Marshal(r)
	if err != nil || len(data) > 48*1024 {
		r.Rows = []SearchHitDTO{}
		r.State = "error"
		r.Partial = true
		r.Code = "response_too_large"
	}
	return r
}
