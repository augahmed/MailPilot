package ai

import (
	"context"

	"mailpilot.local/core/internal/domain"
)

// Settings contains only non-secret account configuration shared by AI use cases.
// Provider credentials are owned by the OS credential store, never this record.
type Settings struct {
	Model   string
	Enabled bool
}

type SettingsStore interface {
	Settings(context.Context, domain.AccountID) (Settings, error)
	SaveSettings(context.Context, domain.AccountID, Settings) error
}
