// Package app implements use cases independently of Tauri and the wire protocol.
package app

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

type Database interface {
	Ping(context.Context) error
	SchemaVersion() int
}

type App struct {
	db      Database
	version string
}

func New(db Database, version string) *App { return &App{db: db, version: version} }

func (a *App) Health(ctx context.Context) domain.Health {
	status := "connected"
	if err := a.db.Ping(ctx); err != nil {
		status = "disconnected"
	}
	return domain.Health{Name: "MailPilot", Version: a.version, ProtocolVersion: domain.ProtocolVersion, Backend: "connected", Database: status, SchemaVersion: a.db.SchemaVersion()}
}
