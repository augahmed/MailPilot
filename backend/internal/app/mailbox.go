package app

import (
	"context"
	"strings"

	"mailpilot.local/core/internal/repository"
	mailsync "mailpilot.local/core/internal/sync"
)

func (a *App) SyncStart() mailsync.Progress  { return a.Sync.Start() }
func (a *App) SyncStatus() mailsync.Progress { return a.Sync.Status() }

type MailboxRow struct {
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	Subject    string `json:"subject"`
	ReceivedAt int64  `json:"receivedAt"`
	BodyState  string `json:"bodyState"`
}
type MailboxPage struct {
	AccountID string       `json:"accountId"`
	Rows      []MailboxRow `json:"rows"`
	HasMore   bool         `json:"hasMore"`
	Code      string       `json:"code,omitempty"`
}

func shortText(value string, limit int) string {
	r := []rune(strings.ToValidUTF8(value, "�"))
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return string(r)
}
func (a *App) Mailbox(ctx context.Context, offset int) MailboxPage {
	result := MailboxPage{Rows: []MailboxRow{}}
	account, session, err := a.Auth.Session()
	if err != nil {
		return result
	}
	result.AccountID = string(account)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	rows, err := a.Emails.ListSummaries(ctx, account, repository.Page{Limit: 11, Offset: offset})
	if err != nil {
		result.Code = "storage_error"
		return result
	}
	result.HasMore = len(rows) > 10
	for _, e := range rows[:min(10, len(rows))] {
		sender := e.Sender.Name
		if sender == "" {
			sender = e.Sender.Address
		}
		result.Rows = append(result.Rows, MailboxRow{ID: shortText(string(e.ID), 100), Sender: shortText(sender, 100), Subject: shortText(e.Subject, 200), ReceivedAt: e.ReceivedAt.UnixMilli(), BodyState: e.BodyState})
	}
	if session.Err() != nil {
		return MailboxPage{Rows: []MailboxRow{}}
	}
	return result
}
