package app

import (
	"context"
	"errors"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

// Shared safe wire codes for optional AI workflows, including reviewed drafts.
// Unknown errors never expose provider response bodies or mailbox data.
func aiWorkflowCode(e error) string {
	if e == nil {
		return ""
	}
	switch {
	case errors.Is(e, repository.ErrConflict), errors.Is(e, ai.ErrConsent):
		return "stale_preview"
	case errors.Is(e, repository.ErrNotFound):
		return "not_found"
	case errors.Is(e, repository.ErrInvalid), errors.Is(e, ai.ErrInvalid), errors.Is(e, provider.ErrInvalidArgument):
		return "validation"
	case errors.Is(e, credentials.ErrUnavailable), errors.Is(e, credentials.ErrNotFound), errors.Is(e, ai.ErrCredentials):
		return "ai_credentials_unavailable"
	case errors.Is(e, ai.ErrDisabled):
		return "ai_disabled"
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(e, provider.ErrUnsupported):
		return "unsupported"
	case errors.Is(e, provider.ErrAuthenticationRequired):
		return "reauth_required"
	case errors.Is(e, ai.ErrAuthentication), errors.Is(e, ai.ErrRateLimited), errors.Is(e, ai.ErrRefused), errors.Is(e, ai.ErrIncomplete):
		return ai.SafeError(e).Error()
	default:
		return "operation_failed"
	}
}
