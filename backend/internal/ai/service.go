package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"mailpilot.local/core/internal/domain"
)

// Identity is immutable trusted configuration, disclosed before approval.
// Future local adapters can set External false; approval is still explicit.
type Identity struct {
	Provider, Model string
	External        bool
}
type Preview struct {
	ID           string
	AccountID    domain.AccountID
	Identity     Identity
	Instructions Text
	Request      SummaryRequest
	ExpiresAt    time.Time
}

// Service is a bounded in-memory consent boundary. The caller must authorize
// the account/session before Prepare AND Execute, and Clear on disconnect.
// The summary use case discloses the complete bounded preview through typed IPC.
type Service struct {
	mu         sync.Mutex
	provider   AIProvider
	identity   Identity
	pending    *Preview
	busy       bool
	cancel     context.CancelFunc
	generation uint64
	now        func() time.Time
}

// NewService(nil, Identity{}) creates a cleanly disabled service. Adapters are
// immutable; replace the service and Clear the old one when configuration changes.
func NewService(provider AIProvider, identity Identity) (*Service, error) {
	if provider != nil && (strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.Model) == "" || len(identity.Provider) > 64 || len(identity.Model) > 128) {
		return nil, ErrInvalid
	}
	return &Service{provider: provider, identity: identity, now: time.Now}, nil
}

func (s *Service) Enabled() bool { return s != nil && s.provider != nil }

func (s *Service) Prepare(ctx context.Context, account domain.AccountID, request SummaryRequest) (Preview, error) {
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	if !s.Enabled() {
		return Preview{}, ErrDisabled
	}
	if account == "" || len(account) > 256 {
		return Preview{}, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return Preview{}, err
	}

	instructions := Instructions()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return Preview{}, ErrBusy
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Preview{}, ErrUnavailable
	}
	p := Preview{hex.EncodeToString(token[:]), account, s.identity, NewText(instructions), request, s.now().Add(5 * time.Minute)}
	s.pending = &p
	return p, nil
}

// Execute consumes a matching approval before dispatch, including failed or
// uncertain requests. No automatic retries; a new preview is needed to retry.
func (s *Service) Execute(ctx context.Context, account domain.AccountID, previewID string, approved bool) (SummaryResult, error) {
	if err := ctx.Err(); err != nil {
		return SummaryResult{}, err
	}
	if !s.Enabled() {
		return SummaryResult{}, ErrDisabled
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return SummaryResult{}, ErrBusy
	}
	p := s.pending
	if p != nil && !s.now().Before(p.ExpiresAt) {
		s.pending = nil
		p = nil
	}
	if !approved || p == nil || p.ID != previewID || p.AccountID != account {
		s.mu.Unlock()
		return SummaryResult{}, ErrConsent
	}
	s.pending = nil
	s.busy = true
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	s.cancel = cancel
	generation := s.generation
	s.mu.Unlock()
	defer cancel()
	result, err := s.provider.SummarizeEmail(ctx, p.Request)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy, s.cancel = false, nil
	if generation != s.generation {
		return SummaryResult{}, ErrConsent
	}
	if ctx.Err() != nil {
		return SummaryResult{}, ctx.Err()
	}
	if err != nil {
		return SummaryResult{}, SafeError(err)
	}
	if err := result.Validate(p.Request); err != nil {
		return SummaryResult{}, err
	}
	return result, nil
}

// Clear removes sensitive pending input, revokes approval and cancels an active
// call. A dispatched external request may already have reached its provider.
func (s *Service) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	s.generation++
	if s.cancel != nil {
		s.cancel()
	}
}
