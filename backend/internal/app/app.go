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

// Authentication supplies session authorization; Gmail token authority remains
// on auth.Manager and is never part of the synthetic implementation.
type Authentication interface {
	Status() auth.Status
	Begin() auth.BeginResult
	Check() auth.Status
	Disconnect() auth.Status
	Cancel() auth.Status
	Session() (domain.AccountID, context.Context, error)
}

type App struct {
	Demo           bool
	Search         *LocalSearch
	Index          *LocalIndex
	AI             *AIService
	Reader         *CachedReader
	Replies        *ReplyService
	RiskEngine     *risk.Engine
	Auth           Authentication
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
	if a.Search != nil {
		a.Search.Clear()
	}
	if a.Index != nil {
		a.Index.Clear()
	}
	if a.AI != nil {
		a.AI.Clear()
	}
	if a.Replies != nil {
		a.Replies.Clear()
	}
	if a.Sync != nil {
		a.Sync.Clear()
	}
	return a.Auth.Begin()
}
func (a *App) AuthCheck() auth.Status { return a.Auth.Check() }
func (a *App) AuthDisconnect() auth.Status {
	if a.Search != nil {
		a.Search.Clear()
	}
	if a.Index != nil {
		a.Index.Clear()
	}
	if a.AI != nil {
		a.AI.Clear()
	}
	if a.Replies != nil {
		a.Replies.Clear()
	}
	if a.Sync != nil {
		a.Sync.Clear()
	}
	return a.Auth.Disconnect()
}

func (a *App) AuthCancel() auth.Status { return a.Auth.Cancel() }
