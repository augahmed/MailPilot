package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
	mailsync "mailpilot.local/core/internal/sync"
)

func (a *App) SyncStart() mailsync.Progress  { return a.Sync.Start() }
func (a *App) SyncStatus() mailsync.Progress { return a.Sync.Status() }

type MailboxRow struct {
	ID         string   `json:"id"`
	Sender     string   `json:"sender"`
	Subject    string   `json:"subject"`
	ReceivedAt int64    `json:"receivedAt"`
	BodyState  string   `json:"bodyState"`
	Phishing   *RiskDTO `json:"phishing,omitempty"`
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
		assessment := a.mailRisk(ctx, account, e.ID)
		result.Rows = append(result.Rows, MailboxRow{Phishing: assessment, ID: shortText(string(e.ID), 100), Sender: shortText(sender, 100), Subject: shortText(e.Subject, 200), ReceivedAt: e.ReceivedAt.UnixMilli(), BodyState: e.BodyState})
	}
	if session.Err() != nil {
		return MailboxPage{Rows: []MailboxRow{}}
	}
	return result
}

// Risk results are local derived data. Only cache misses or version changes run
// extraction; mailbox polling never invokes a provider or changes membership.
type RiskReasonDTO struct {
	Code        string `json:"code"`
	Points      int    `json:"points"`
	Explanation string `json:"explanation"`
}
type RiskDTO struct {
	Score   int             `json:"score"`
	Risk    string          `json:"risk"`
	Version string          `json:"version"`
	Reasons []RiskReasonDTO `json:"reasons"`
}

func (a *App) mailRisk(ctx context.Context, account domain.AccountID, id domain.EmailID) *RiskDTO {
	engine := a.RiskEngine
	if engine == nil {
		engine = risk.DefaultEngine()
	}
	p, err := assessRisk(ctx, a.Emails, engine, account, id)
	if err != nil {
		return nil
	}
	return riskDTO(p.Score, engine.Level(p.Score), p.AnalyzerVersion, p.Findings)
}
func assessRisk(ctx context.Context, emails repository.EmailRepository, engine *risk.Engine, account domain.AccountID, id domain.EmailID) (domain.PhishingResult, error) {
	p, err := emails.GetPhishingResult(ctx, account, id)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return p, err
	}
	if err != nil || p.AnalyzerVersion != engine.Version() {
		stored, err := emails.Get(ctx, account, id)
		if err != nil {
			return p, err
		}
		f, err := risk.NewPhishingFeatureExtractor().Extract(ctx, stored.Email)
		if err != nil {
			return p, err
		}
		p, err = engine.Analyze(ctx, f, stored.ContentHash, time.Now().UTC())
		if err != nil {
			return p, err
		}
		if err = emails.SavePhishingResult(ctx, p); err != nil {
			return p, err
		}
	}
	return p, nil
}
func riskDTO(score int, level, version string, findings []domain.PhishingFinding) *RiskDTO {
	dto := &RiskDTO{Score: score, Risk: level, Version: version, Reasons: []RiskReasonDTO{}}
	for _, f := range findings {
		dto.Reasons = append(dto.Reasons, RiskReasonDTO{f.Code, f.Points, f.Explanation})
	}
	return dto
}
