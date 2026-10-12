package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

// Secret cannot be accidentally formatted, logged, or JSON-encoded as plaintext.
// Reveal is reserved for credential adapters and OAuth/AI network boundaries.
type Secret struct{ value string }

func NewSecret(value string) Secret         { return Secret{value: value} }
func (s Secret) Reveal() string             { return s.value }
func (s Secret) Empty() bool                { return s.value == "" }
func (Secret) String() string               { return "[REDACTED]" }
func (Secret) GoString() string             { return "[REDACTED]" }
func (Secret) Format(w fmt.State, _ rune)   { _, _ = io.WriteString(w, "[REDACTED]") }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

var ErrNotFound = errors.New("credential_missing")
var ErrUnavailable = errors.New("credential_store_unavailable")

// Record contains only a refresh token and non-secret local identity. Access
// tokens live in memory. Never serialize Record to a file or IPC response.
type Record struct {
	RefreshToken Secret
	AccountID    string
	Email        string
}

func (Record) String() string       { return "[credential record]" }
func (Record) GoString() string     { return "[credential record]" }
func (Record) LogValue() slog.Value { return slog.StringValue("[credential record]") }

type TokenStore interface {
	Load(context.Context) (Record, error)
	Save(context.Context, Record) error
	Delete(context.Context) error
}
