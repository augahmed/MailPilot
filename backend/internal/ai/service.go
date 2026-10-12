package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	Operation    Operation
	Instructions Text
	Request      Request
	ExpiresAt    time.Time
}

// Service is a bounded in-memory consent boundary. The caller must authorize
// the account/session before Prepare AND Execute, and Clear on disconnect.
// No IPC exposure is provided until a UI can display the complete preview.
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

func (s *Service) Prepare(ctx context.Context, account domain.AccountID, op Operation, request Request) (Preview, error) {
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
	if (op == Summary || op == Answer) && strings.TrimSpace(request.Context.Reveal()) == "" {
		return Preview{}, ErrInvalid
	}
	instructions, err := Instructions(op)
	if err != nil {
		return Preview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return Preview{}, ErrBusy
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return Preview{}, ErrUnavailable
	}
	p := Preview{hex.EncodeToString(token[:]), account, s.identity, op, NewText(instructions), request, s.now().Add(5 * time.Minute)}
	s.pending = &p
	return p, nil
}

// Execute consumes a matching approval before dispatch, including failed or
// uncertain requests. No automatic retries; a new preview is needed to retry.
func (s *Service) Execute(ctx context.Context, account domain.AccountID, previewID string, approved bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !s.Enabled() {
		return Result{}, ErrDisabled
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return Result{}, ErrBusy
	}
	p := s.pending
	if p != nil && !s.now().Before(p.ExpiresAt) {
		s.pending = nil
		p = nil
	}
	if !approved || p == nil || p.ID != previewID || p.AccountID != account {
		s.mu.Unlock()
		return Result{}, ErrConsent
	}
	s.pending = nil
	s.busy = true
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	s.cancel = cancel
	generation := s.generation
	s.mu.Unlock()
	defer cancel()
	var result Result
	var err error
	switch p.Operation {
	case Draft:
		result, err = s.provider.GenerateDraft(ctx, p.Request)
	case Summary:
		result, err = s.provider.SummarizeThread(ctx, p.Request)
	case Answer:
		result, err = s.provider.AnswerWithContext(ctx, p.Request)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy, s.cancel = false, nil
	if generation != s.generation {
		return Result{}, ErrConsent
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		return Result{}, SafeError(err)
	}
	t := result.Text.Reveal()
	if strings.TrimSpace(t) == "" || len(t) > MaxOutputBytes || !utf8.ValidString(t) || strings.ContainsRune(t, '\x00') {
		return Result{}, ErrResponse
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
