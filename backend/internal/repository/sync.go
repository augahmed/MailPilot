package repository

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

// Cursor belongs to the adapter. Phase/After persist bounded recovery progress.
// Record/Offset allow a single large history record to resume without skipping it.
type SyncCheckpoint struct {
	Cursor, Phase, After, Record string
	Offset                       int
	Limited                      bool
}
type SyncRepository interface {
	Load(context.Context, domain.AccountID) (SyncCheckpoint, error)
	// Commit atomically applies one observation and its checkpoint. Expected is a
	// compare-and-swap guard against stale/concurrent workers. Nil mail is allowed.
	Commit(context.Context, domain.AccountID, SyncCheckpoint, SyncCheckpoint, *domain.Email, domain.ProviderMessageID) error
	CachedIDs(context.Context, domain.AccountID, string, int) ([]domain.ProviderMessageID, error)
}
