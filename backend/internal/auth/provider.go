package auth

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"time"
)

var (
	ErrFailed  = errors.New("authentication_failed")
	ErrDenied  = errors.New("consent_denied")
	ErrExpired = errors.New("reauth_required")
	ErrScope   = errors.New("required_scope_missing")
	ErrTimeout = errors.New("authentication_timeout")
)

type Grant struct {
	AccessToken  credentials.Secret
	RefreshToken credentials.Secret
	Expiry       time.Time
}
type Provider interface {
	Configured() bool
	AuthorizationURL(redirect, state, verifier string) string
	Exchange(context.Context, string, string, string) (Grant, error)
	Refresh(context.Context, credentials.Secret) (Grant, error)
	Email(context.Context, credentials.Secret) (string, error)
	Revoke(context.Context, credentials.Secret) error
}

// Status is the only authentication information exposed to the UI.
type Status struct {
	AccountID domain.AccountID `json:"-"`
	State     string           `json:"state"`
	Email     string           `json:"email,omitempty"`
	Code      string           `json:"code,omitempty"`
}
type BeginResult struct {
	Status           Status `json:"status"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"` // host only, never frontend
}
