// Package app implements use cases independently of Tauri and the wire protocol.
package app

import (
	"context"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
	mailsync "mailpilot.local/core/internal/sync"
)

type Database interface {
	Ping(context.Context) error
	SchemaVersion() int
}

type App struct {
	Replies        *ReplyService
	Search         *MailboxSearch
	RiskEngine     *risk.Engine
	Auth           *auth.Manager
	Sync           *mailsync.Service
	Emails         repository.EmailRepository
	Categories     repository.CategoryRepository
	Classification *ClassificationReview
	Organization   *Organization
	db             Database
	version        string
}

func New(db Database, version string) *App { return &App{db: db, version: version} }

func (a *App) Health(ctx context.Context) domain.Health {
	status := "connected"
	if err := a.db.Ping(ctx); err != nil {
		status = "disconnected"
	}
	return domain.Health{Name: "MailPilot", Version: a.version, ProtocolVersion: domain.ProtocolVersion, Backend: "connected", Database: status, SchemaVersion: a.db.SchemaVersion()}
}

func (a *App) AuthStatus() auth.Status { return a.Auth.Status() }
func (a *App) AuthBegin() auth.BeginResult {
	if a.Replies != nil {
		a.Replies.Clear()
	}
	if a.Search != nil {
		a.Search.Clear()
	}
	if a.Sync != nil {
		a.Sync.Clear()
	}
	return a.Auth.Begin()
}
func (a *App) AuthCheck() auth.Status { return a.Auth.Check() }
func (a *App) AuthDisconnect() auth.Status {
	if a.Replies != nil {
		a.Replies.Clear()
	}
	if a.Search != nil {
		a.Search.Clear()
	}
	if a.Sync != nil {
		a.Sync.Clear()
	}
	return a.Auth.Disconnect()
}

func (a *App) AuthCancel() auth.Status { return a.Auth.Cancel() }
