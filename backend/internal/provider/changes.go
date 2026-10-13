package provider

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
)

var ErrCheckpointExpired = errors.New("provider checkpoint expired")

// Changes are opaque account-bound observations, never remote write commands.
type MessageChange struct {
	ProviderID domain.ProviderMessageID
	Deleted    bool
}
type ChangeRecord struct {
	Cursor  string
	Changes []MessageChange
}
type ChangePage struct {
	Records               []ChangeRecord
	NextPageToken, Cursor string
}
type ChangeReader interface {
	MetadataReader
	CurrentCheckpoint(context.Context) (string, error)
	ListChanges(context.Context, string, PageOptions) (ChangePage, error)
}
