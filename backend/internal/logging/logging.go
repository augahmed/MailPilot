// Package logging configures structured diagnostics on stderr, never protocol stdout.
package logging

import (
	"io"
	"log/slog"
)

func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})).With("component", "mailpilot-core")
}
