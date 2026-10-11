package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

// Manager owns one account's OAuth lifecycle. Network/keyring work runs off the
// IPC thread. opMu serializes refresh, credential writes, and disconnect cleanup.
type Manager struct {
	mu            sync.Mutex
	opMu          sync.Mutex
	provider      Provider
	store         credentials.TokenStore
	accounts      repository.AccountRepository
	reference     string
	root          context.Context
	stop          context.CancelFunc
	cancel        context.CancelFunc
	generation    uint64
	busy          bool
	closed        bool
	status        Status
	grant         Grant
	session       context.Context
	sessionCancel context.CancelFunc
	accountID     domain.AccountID
}

func NewManager(parent context.Context, p Provider, store credentials.TokenStore, accounts repository.AccountRepository, reference string) *Manager {
	ctx, stop := context.WithCancel(parent)
	m := &Manager{provider: p, store: store, accounts: accounts, reference: reference, root: ctx, stop: stop, status: Status{State: "disconnected"}}
	if !p.Configured() {
		m.status = Status{State: "unconfigured", Code: "oauth_not_configured"}
	}
	return m
}
func (m *Manager) Close() {
	m.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.endSessionLocked()
	m.grant = Grant{}
	m.closed = true
	m.busy = false
	m.generation++
	m.status = Status{State: "disconnected"}
}
func (m *Manager) startLocked(state string, work func(context.Context) (Status, Grant)) context.Context {
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	id := m.generation
	ctx, cancel := context.WithTimeout(m.root, 3*time.Minute)
	m.cancel = cancel
	m.busy = true
	m.status = Status{State: state}
	m.grant = Grant{}
	go func() {
		defer cancel()
		m.opMu.Lock()
		status, grant := work(ctx)
		m.opMu.Unlock()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.generation == id {
			// Serialize publication with Cancel: a cancelled job must never
			// publish connected after cancellation acquired this same mutex.
			if ctx.Err() != nil && status.State == "connected" {
				status, grant = failure(ctx.Err()), Grant{}
			}
			if status.State == "connected" {
				if m.session == nil || m.session.Err() != nil || m.accountID != status.AccountID {
					m.endSessionLocked()
					m.session, m.sessionCancel = context.WithCancel(m.root)
				}
				m.accountID = status.AccountID
			} else if sessionRejected(status) {
				m.endSessionLocked()
			}
			m.status = status
			m.grant = grant
			m.busy = false
		}
	}()
	return ctx
}

// Definitive credential rejection ends account authorization. A temporary
// provider/network failure does not revoke an already authorized local cache;
// remote requests still require successful token retrieval.
func sessionRejected(status Status) bool {
	return status.State == "disconnected" || status.State == "reauth_required" || status.Code == "credential_store_unavailable" || status.Code == "required_scope_missing" || status.Code == "consent_denied"
}
func failure(err error) Status {
	code := "authentication_failed"
	switch {
	case errors.Is(err, credentials.ErrUnavailable):
		code = "credential_store_unavailable"
	case errors.Is(err, ErrDenied):
		code = "consent_denied"
	case errors.Is(err, ErrScope):
		code = "required_scope_missing"
	case errors.Is(err, ErrExpired):
		return Status{State: "reauth_required", Code: "reauth_required"}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrTimeout):
		code = "authentication_timeout"
	case errors.Is(err, context.Canceled):
		code = "authentication_cancelled"
	}
	return Status{State: "error", Code: code}
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && !m.busy && m.status.State == "connected" && time.Until(m.grant.Expiry) < time.Minute {
		m.startLocked("checking", m.restore)
	}
	return m.status
}
func (m *Manager) Check() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && !m.busy && m.provider.Configured() {
		m.startLocked("checking", m.restore)
	}
	return m.status
}
func (m *Manager) Begin() BeginResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.provider.Configured() || m.busy || m.status.State == "connected" {
		return BeginResult{Status: m.status}
	}
	l, err := newLoopback()
	if err != nil {
		m.status = failure(err)
		return BeginResult{Status: m.status}
	}
	m.endSessionLocked()
	url := m.provider.AuthorizationURL(l.redirect, l.state, l.verifier)
	sessionContext := m.startLocked("connecting", func(ctx context.Context) (Status, Grant) {
		defer l.close()
		if ctx.Err() != nil {
			return failure(ctx.Err()), Grant{}
		}
		// Check access before exchanging a code. No insecure fallback on a locked store.
		previous, err := m.store.Load(ctx)
		if err != nil && !errors.Is(err, credentials.ErrNotFound) {
			return failure(err), Grant{}
		}
		code, err := l.wait(ctx)
		if err != nil {
			return failure(err), Grant{}
		}
		grant, err := m.provider.Exchange(ctx, code, l.redirect, l.verifier)
		if err != nil {
			return failure(err), Grant{}
		}
		email, err := m.provider.Email(ctx, grant.AccessToken)
		if err != nil {
			return failure(err), Grant{}
		}
		if !validEmail(email) || grant.RefreshToken.Empty() {
			return failure(ErrFailed), Grant{}
		}
		if ctx.Err() != nil {
			return failure(ctx.Err()), Grant{}
		}
		id, err := m.accounts.Upsert(ctx, domain.Account{Provider: "gmail", ProviderAccountID: email, Address: domain.EmailAddress{Address: email}, CredentialRef: m.reference})
		if err != nil {
			return Status{State: "error", Code: "account_storage_failed"}, Grant{}
		}
		record := credentials.Record{RefreshToken: grant.RefreshToken, AccountID: string(id), Email: email}
		if err = m.store.Save(ctx, record); err != nil {
			return failure(err), Grant{}
		}
		if previous.AccountID != "" && previous.AccountID != record.AccountID {
			m.clearReference(ctx, previous.AccountID)
		}
		return Status{State: "connected", Email: email, AccountID: id}, grant
	})
	// Native keyring prompts may not honor cancellation. Close the callback
	// independently so even a blocked credential operation cannot extend it.
	go func() { <-sessionContext.Done(); l.close() }()
	return BeginResult{Status: m.status, AuthorizationURL: url}
}
func (m *Manager) restore(ctx context.Context) (Status, Grant) {
	record, err := m.store.Load(ctx)
	if errors.Is(err, credentials.ErrNotFound) {
		return Status{State: "disconnected"}, Grant{}
	}
	if err != nil {
		return failure(err), Grant{}
	}
	grant, err := m.provider.Refresh(ctx, record.RefreshToken)
	if err != nil {
		return failure(err), Grant{}
	}
	// Persist rotated refresh tokens before other network requests can fail.
	record.RefreshToken = grant.RefreshToken
	if err = m.store.Save(ctx, record); err != nil {
		return failure(err), Grant{}
	}
	email, err := m.provider.Email(ctx, grant.AccessToken)
	if err != nil {
		return failure(err), Grant{}
	}
	if !validEmail(email) || email != record.Email {
		return failure(ErrExpired), Grant{}
	}
	if ctx.Err() != nil {
		return failure(ctx.Err()), Grant{}
	}
	_, err = m.accounts.Upsert(ctx, domain.Account{ID: domain.AccountID(record.AccountID), Provider: "gmail", ProviderAccountID: email, Address: domain.EmailAddress{Address: email}, CredentialRef: m.reference})
	if err != nil {
		return Status{State: "error", Code: "account_storage_failed"}, Grant{}
	}
	return Status{State: "connected", Email: email, AccountID: domain.AccountID(record.AccountID)}, grant
}
func (m *Manager) clearReference(ctx context.Context, id string) bool {
	account, err := m.accounts.Get(ctx, domain.AccountID(id))
	if errors.Is(err, repository.ErrNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	account.CredentialRef = ""
	_, err = m.accounts.Upsert(ctx, account)
	return err == nil
}
func (m *Manager) Disconnect() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.status.State == "disconnecting" {
		return m.status
	}
	m.endSessionLocked()
	m.startLocked("disconnecting", func(ctx context.Context) (Status, Grant) {
		record, err := m.store.Load(ctx)
		if err != nil && !errors.Is(err, credentials.ErrNotFound) {
			return failure(err), Grant{}
		}
		// Delete locally first, even offline. A failed deletion is not disconnected.
		if err = m.store.Delete(ctx); err != nil {
			return failure(err), Grant{}
		}
		status := Status{State: "disconnected"}
		if record.AccountID != "" && !m.clearReference(ctx, record.AccountID) {
			status.Code = "account_cleanup_failed"
		}
		if !record.RefreshToken.Empty() {
			if m.provider.Revoke(ctx, record.RefreshToken) != nil {
				status.Code = "revocation_failed"
			}
		}
		return status, Grant{}
	})
	return m.status
}

// Cancel only stops an unfinished sign-in (for example a browser launch failure).
// It does not remove or revoke a previous credential.
func (m *Manager) Cancel() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "connecting" && m.cancel != nil {
		m.cancel()
	}
	return m.status
}

func (*Manager) Format(w fmt.State, _ rune) { _, _ = io.WriteString(w, "[authentication manager]") }
func (*Manager) LogValue() slog.Value       { return slog.StringValue("[authentication manager]") }

func (m *Manager) endSessionLocked() {
	if m.sessionCancel != nil {
		m.sessionCancel()
	}
	m.session = nil
	m.accountID = ""
}

// Session exposes account identity and cancellation, never token values. The
// context is cancelled immediately on disconnect, replacement, or shutdown.
func (m *Manager) Session() (domain.AccountID, context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.session == nil || m.session.Err() != nil || m.accountID == "" {
		return "", nil, ErrExpired
	}
	return m.accountID, m.session, nil
}

// AccessToken is an internal adapter boundary; it is never registered with IPC.
func (m *Manager) AccessToken(ctx context.Context, account domain.AccountID) (credentials.Secret, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.closed || m.accountID != account || m.session == nil || m.session.Err() != nil {
		m.mu.Unlock()
		return credentials.Secret{}, ErrExpired
	}
	generation := m.generation
	grant := m.grant
	m.mu.Unlock()
	if ctx.Err() != nil {
		return credentials.Secret{}, ctx.Err()
	}
	if !grant.AccessToken.Empty() && time.Until(grant.Expiry) > time.Minute {
		return grant.AccessToken, nil
	}
	status, renewed := m.restore(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.accountID != account || m.closed || ctx.Err() != nil {
		return credentials.Secret{}, ErrExpired
	}
	if status.State != "connected" || status.AccountID != account {
		m.status = status
		m.grant = Grant{}
		if sessionRejected(status) {
			m.endSessionLocked()
		}
		return credentials.Secret{}, ErrExpired
	}
	m.status = status
	m.grant = renewed
	return renewed.AccessToken, nil
}
