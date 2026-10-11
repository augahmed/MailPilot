// Package sync coordinates bounded imports without provider-specific structures.
package sync

import (
	"context"
	"errors"
	"sync"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

type Reader interface {
	ListMessages(context.Context, provider.ListMessagesOptions) (provider.MessagePage, error)
	FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error)
}
type Session func() (domain.AccountID, context.Context, error)
type Factory func(domain.AccountID) Reader

type Progress struct {
	State      string           `json:"state"`
	AccountID  domain.AccountID `json:"accountId"`
	Limit      int              `json:"limit"`
	Discovered int              `json:"discovered"`
	Imported   int              `json:"imported"`
	Updated    int              `json:"updated"`
	Errors     int              `json:"errors"`
	Code       string           `json:"code,omitempty"`
	Capped     bool             `json:"capped"`
}
type Service struct {
	gate       chan struct{}
	mu         sync.Mutex
	root       context.Context
	session    Session
	factory    Factory
	emails     repository.EmailRepository
	limit      int
	progress   Progress
	cancel     context.CancelFunc
	generation uint64
}

func New(root context.Context, session Session, factory Factory, emails repository.EmailRepository, limit int) *Service {
	return &Service{root: root, session: session, factory: factory, emails: emails, limit: limit, progress: Progress{State: "idle", Limit: limit}}
}

// SetOperationGate is configured once before starting jobs; use a capacity-one channel.
func (s *Service) SetOperationGate(gate chan struct{}) { s.gate = gate }
func (s *Service) Status() Progress                    { s.mu.Lock(); defer s.mu.Unlock(); return s.progress }
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.generation++
	s.progress = Progress{State: "idle", Limit: s.limit}
}
func (s *Service) Start() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.progress.State == "running" {
		return s.progress
	}
	account, session, err := s.session()
	if err != nil {
		s.progress = Progress{State: "error", Limit: s.limit, Code: "reauth_required"}
		return s.progress
	}
	if s.limit < 1 || s.limit > 10000 {
		s.progress = Progress{State: "error", Limit: s.limit, Code: "invalid_limit"}
		return s.progress
	}
	ctx, cancel := context.WithTimeout(s.root, 10*time.Minute)
	s.cancel = cancel
	stop := context.AfterFunc(session, cancel)
	s.generation++
	generation := s.generation
	s.progress = Progress{State: "running", AccountID: account, Limit: s.limit}
	go func() { defer cancel(); defer stop(); s.run(ctx, account, generation, s.factory(account)) }()
	return s.progress
}
func (s *Service) update(g uint64, fn func(*Progress)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == g {
		fn(&s.progress)
	}
}
func (s *Service) run(ctx context.Context, account domain.AccountID, g uint64, reader Reader) {
	state := "completed"
	defer func() {
		if ctx.Err() != nil {
			state = "cancelled"
		}
		s.update(g, func(p *Progress) { p.State = state })
	}()
	if s.gate != nil {
		select {
		case s.gate <- struct{}{}:
			defer func() { <-s.gate }()
		case <-ctx.Done():
			return
		}
	}
	report := func(code string) { s.update(g, func(p *Progress) { p.Errors++; p.Code = code }) }
	seen := map[domain.ProviderMessageID]bool{}
	pages := map[string]bool{}
	token := ""
	for len(seen) < s.limit {
		if ctx.Err() != nil {
			return
		}
		if pages[token] {
			report("pagination_error")
			state = "error"
			return
		}
		pages[token] = true
		size := min(100, s.limit-len(seen))
		page, err := reader.ListMessages(ctx, provider.ListMessagesOptions{Page: provider.PageOptions{Limit: size, PageToken: token}})
		if err != nil {
			if ctx.Err() == nil {
				report(errorCode(err))
				state = "error"
			}
			return
		}
		if len(page.Messages) > size || len(page.Messages) == 0 && page.NextPageToken != "" {
			report("pagination_error")
			state = "error"
			return
		}
		for _, ref := range page.Messages {
			if ctx.Err() != nil {
				return
			}
			if seen[ref.ProviderID] {
				continue
			}
			if ref.AccountID != account || ref.ProviderID == "" {
				report("provider_error")
				state = "error"
				return
			}
			seen[ref.ProviderID] = true
			s.update(g, func(p *Progress) { p.Discovered++ })
			old, err := s.emails.GetByProviderID(ctx, account, ref.ProviderID)
			exists := err == nil
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				report("storage_error")
				continue
			}
			var email domain.Email
			if exists && old.Email.BodyState == "complete" {
				if inspector, ok := reader.(provider.MetadataReader); ok {
					metadata, inspectErr := inspector.FetchMessageMetadata(ctx, ref.ProviderID)
					if inspectErr != nil {
						if ctx.Err() != nil {
							return
						}
						report(errorCode(inspectErr))
						if errors.Is(inspectErr, provider.ErrAuthenticationRequired) {
							state = "error"
							return
						}
						continue
					}
					if metadata.ProviderID != ref.ProviderID {
						report("provider_error")
						continue
					}
					if metadata.ContentRevision != "" && metadata.ContentRevision == old.Email.ContentRevision {
						email = old.Email
						email.Labels = metadata.Labels
						email.ProviderThreadID = metadata.ProviderThreadID
					}
				}
			}
			if email.ProviderID == "" {
				email, err = reader.FetchMessage(ctx, ref.ProviderID)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					report(errorCode(err))
					if errors.Is(err, provider.ErrAuthenticationRequired) {
						state = "error"
						return
					}
					continue
				}
			}
			if email.AccountID != account || email.ProviderID != ref.ProviderID {
				report("provider_error")
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if _, err = s.emails.Upsert(ctx, email); err != nil {
				if ctx.Err() != nil {
					return
				}
				report("storage_error")
				continue
			}
			s.update(g, func(p *Progress) {
				if exists {
					p.Updated++
				} else {
					p.Imported++
				}
			})
			if email.BodyState != "complete" {
				report("body_unavailable")
			}
		}
		if len(seen) >= s.limit {
			s.update(g, func(p *Progress) { p.Capped = page.NextPageToken != "" })
			return
		}
		if page.NextPageToken == "" {
			return
		}
		token = page.NextPageToken
		if len(pages) > s.limit+1 {
			report("pagination_error")
			state = "error"
			return
		}
	}
}
func errorCode(err error) string {
	if errors.Is(err, provider.ErrAuthenticationRequired) {
		return "reauth_required"
	}
	if errors.Is(err, provider.ErrRateLimited) {
		return "rate_limited"
	}
	if errors.Is(err, provider.ErrMalformed) {
		return "invalid_message"
	}
	return "provider_error"
}
