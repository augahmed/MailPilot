package app

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/risk"
	"mailpilot.local/core/internal/store/sqlite"
	"testing"
	"time"
)

func TestMailRiskPersistsReusesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	account, err := s.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "fixture", ProviderAccountID: "a", Address: domain.EmailAddress{Address: "a@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	mail := domain.Email{AccountID: account, ProviderID: "one", BodyState: "complete", Sender: domain.EmailAddress{Address: "sender@example.com"}, ReceivedAt: time.Now(), Subject: "Urgent: confirm your password", Headers: []domain.EmailHeader{{Name: "Reply-To", Value: "attacker@evil.test"}}}
	id, err := s.Emails().Upsert(ctx, mail)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Emails: s.Emails()}
	dto := a.mailRisk(ctx, account, id)
	if dto == nil || dto.Score != 48 {
		t.Fatalf("assessment: %+v", dto)
	}
	p, err := s.Emails().GetPhishingResult(ctx, account, id)
	if err != nil || p.AnalyzerVersion != risk.DefaultEngine().Version() {
		t.Fatal(err)
	}
	if dto := a.mailRisk(ctx, "other", id); dto != nil {
		t.Fatal("cross-account result")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Emails = s.Emails()
	if a.mailRisk(ctx, account, id) == nil {
		t.Fatal("restart result missing")
	}
	again, _ := s.Emails().GetPhishingResult(ctx, account, id)
	if !again.AssessedAt.Equal(p.AssessedAt) || again.Findings[0].Points != 25 {
		t.Fatal("cache not reused or reasons lost")
	}
	p.AnalyzerVersion = "old"
	if err = s.Emails().SavePhishingResult(ctx, p); err != nil {
		t.Fatal(err)
	}
	if a.mailRisk(ctx, account, id).Version == "old" {
		t.Fatal("stale version")
	}
	mail.Subject = "Hello"
	mail.Headers = nil
	if _, err = s.Emails().Upsert(ctx, mail); err != nil {
		t.Fatal(err)
	}
	if dto = a.mailRisk(ctx, account, id); dto == nil || dto.Score != 0 {
		t.Fatal("stale content score")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if a.mailRisk(cancelled, account, id) != nil {
		t.Fatal("cancelled assessment")
	}
}
