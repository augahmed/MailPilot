package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
)

func phishingOrganizationFixture(t *testing.T) (*Organization, *organizationFake, domain.Email) {
	t.Helper()
	s, f, email := organizationFixture(t)
	email.Subject = "Urgent: verify your account and confirm your password"
	email.Headers = []domain.EmailHeader{{Name: "From", Value: "sender@example.com"}, {Name: "Reply-To", Value: "reply@different.test"}}
	html := `<a href="https://different.test/login">https://example.com/login</a>`
	email.BodyHTML = &html
	email.BodyText = "Confirm your password"
	email.BodyState = "complete"
	if _, err := s.emails.Upsert(context.Background(), email); err != nil {
		t.Fatal(err)
	}
	f.email = email
	return s, f, email
}
func riskPreview(t *testing.T, s *Organization, email domain.Email, operation string) OrganizationResponse {
	t.Helper()
	r := awaitOrganization(t, s, OrganizationRequest{Operation: operation, Source: "phishing", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if r.Code != "" {
		t.Fatalf("risk preview failed: %+v", r)
	}
	return r
}
func TestPhishingOrganizationReviewDryRunApplyUndo(t *testing.T) {
	s, f, email := phishingOrganizationFixture(t)
	ctx := context.Background()
	list := s.Handle(ctx, OrganizationRequest{Operation: "list", Source: "phishing"})
	if len(list.Rows) != 2 || list.Rows[0].Change != "candidate" || list.Rows[0].Phishing == nil || list.Rows[0].Phishing.Score != 80 || list.Rows[0].Phishing.Threshold != 60 || list.Rows[1].Change != "below_threshold" {
		t.Fatalf("missing review: %+v", list)
	}
	if len(f.writes) != 0 {
		t.Fatal("list wrote Gmail")
	}
	dry := riskPreview(t, s, email, "dry_run")
	if dry.State != "dry_run" || dry.ID != "" || dry.Rows[0].ProposedLabel != domain.PotentialPhishingLabel || len(dry.Steps) != 2 || len(f.writes) != 0 {
		t.Fatal(dry)
	}
	if _, err := s.journal.Latest(ctx, "a", false); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("dry run journaled", err)
	}
	preview := riskPreview(t, s, email, "preview")
	saved, err := s.journal.Get(ctx, "a", domain.ActionID(preview.ID))
	if err != nil || saved.Rows[0].Phishing == nil || saved.Rows[0].CategoryID != "" || len(f.writes) != 0 {
		t.Fatal("missing immutable evidence", err)
	}
	f.beforeWrite = func(kind string) {
		p, err := s.journal.Get(ctx, "a", saved.ID)
		if err != nil || p.Status != domain.ActionApplying {
			t.Error("write not confirmed/journaled")
		}
		found := false
		for _, step := range p.Steps {
			found = found || step.Kind == kind && step.State == domain.ItemInFlight
		}
		if !found {
			t.Error("no dispatch evidence")
		}
	}
	applied := applyOrganization(t, s, preview)
	if applied.Code != "" || applied.State != "applied" || !slices.Equal(f.writes, []string{"create", "add"}) {
		t.Fatalf("apply: %+v writes %v", applied, f.writes)
	}
	f.beforeWrite = nil
	// Undo preserves both system Inbox membership and unrelated later changes.
	later := domain.EmailLabel{AccountID: "a", ProviderID: "later", Name: "Later", Kind: domain.LabelKindUser}
	f.email.Labels = append(f.email.Labels, later)
	f.labels = append(f.labels, later)
	undo := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.State != "preview" || undo.Rows[0].Phishing == nil || len(f.writes) != 2 {
		t.Fatal("undo bypassed review", undo)
	}
	result := applyOrganization(t, s, undo)
	if result.State != "applied" || result.Code != "" {
		t.Fatal(result)
	}
	var ids []domain.ProviderLabelID
	for _, l := range f.email.Labels {
		ids = append(ids, l.ProviderID)
	}
	if !slices.Equal(ids, []domain.ProviderLabelID{"INBOX", "keep", "later"}) {
		t.Fatal("undo removed unrelated membership", ids)
	}
	if !slices.Equal(f.writes, []string{"create", "add", "remove"}) {
		t.Fatal(f.writes)
	}
}
func TestPhishingOrganizationRejectsStaleRiskAndCrossAccount(t *testing.T) {
	for _, change := range []string{"threshold", "content", "live_html", "live_labels", "account", "evidence"} {
		t.Run(change, func(t *testing.T) {
			s, f, email := phishingOrganizationFixture(t)
			preview := riskPreview(t, s, email, "preview")
			switch change {
			case "threshold":
				c := risk.DefaultConfig()
				c.High = 90
				e, err := risk.NewEngine(c)
				if err != nil {
					t.Fatal(err)
				}
				s.SetRiskEngine(e)
			case "content":
				email.Subject = "changed"
				if _, err := s.emails.Upsert(context.Background(), email); err != nil {
					t.Fatal(err)
				}
			case "live_html":
				html := "<p>changed</p>"
				f.email.BodyHTML = &html
			case "live_labels":
				f.email.Labels = f.email.Labels[:1]
			case "account":
				r := awaitOrganization(t, s, OrganizationRequest{Operation: "apply", AccountID: "other", ActionID: preview.ID, Digest: preview.Digest})
				if r.Code != "unauthorized" {
					t.Fatal(r)
				}
				return
			case "evidence":
				p, err := s.emails.GetPhishingResult(context.Background(), "a", email.ID)
				if err != nil {
					t.Fatal(err)
				}
				p.Findings[0].Explanation = "Changed explanation"
				if err = s.emails.SavePhishingResult(context.Background(), p); err != nil {
					t.Fatal(err)
				}
			}
			result := applyOrganization(t, s, preview)
			if result.Code != "stale_preview" || len(f.writes) != 0 {
				t.Fatalf("stale write allowed: %+v %v", result, f.writes)
			}
		})
	}
}
func TestPhishingThresholdEligibilityAndPreexistingLabel(t *testing.T) {
	s, f, email := phishingOrganizationFixture(t)
	c := risk.DefaultConfig()
	c.High = 81
	e, _ := risk.NewEngine(c)
	s.SetRiskEngine(e)
	r := s.Handle(context.Background(), OrganizationRequest{Operation: "list", Source: "phishing"})
	if r.Rows[0].Change != "below_threshold" {
		t.Fatal(r)
	}
	refused := awaitOrganization(t, s, OrganizationRequest{Operation: "preview", Source: "phishing", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if refused.Code != "stale_preview" {
		t.Fatal("below threshold selected", refused)
	}
	c.High = 80
	e, _ = risk.NewEngine(c)
	s.SetRiskEngine(e)
	label := domain.EmailLabel{AccountID: "a", ProviderID: "existing", Name: domain.PotentialPhishingLabel, Kind: domain.LabelKindUser}
	f.labels = append(f.labels, label)
	f.email.Labels = append(f.email.Labels, label)
	preview := riskPreview(t, s, email, "preview")
	if preview.Rows[0].Phishing.Threshold != 80 || preview.Rows[0].Change != "none" || len(preview.Steps) != 0 {
		t.Fatal(preview)
	}
	applied := applyOrganization(t, s, preview)
	if applied.State != "applied" || len(f.writes) != 0 {
		t.Fatal(applied)
	}
	undo := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.Code != "not_found" {
		t.Fatal("preexisting label became undoable", undo)
	}
}
func TestPhishingUncertainWriteUsesExistingReconciliation(t *testing.T) {
	s, f, email := phishingOrganizationFixture(t)
	f.fail = "add"
	preview := riskPreview(t, s, email, "preview")
	result := applyOrganization(t, s, preview)
	if result.State != "uncertain" {
		t.Fatal(result)
	}
	replay := applyOrganization(t, s, preview)
	if replay.Code != "reconciliation_required" || len(f.writes) != 2 {
		t.Fatal("blind retry", replay)
	}
	reconciled := awaitOrganization(t, s, OrganizationRequest{Operation: "reconcile", AccountID: "a"})
	if reconciled.State != "partial" || len(f.writes) != 2 {
		t.Fatal(reconciled)
	}
	undo := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.Code != "not_found" {
		t.Fatal("uncertain ownership undo", undo)
	}
}
func TestOrganizationSourceValidation(t *testing.T) {
	for _, p := range []OrganizationRequest{{Operation: "list", Source: "spam"}, {Operation: "apply", Source: "phishing", AccountID: "a", ActionID: "x", Digest: string(make([]byte, 64))}, {Operation: "undo_preview", Source: "phishing", AccountID: "a"}} {
		if p.Valid() {
			t.Fatal("invalid source accepted")
		}
	}
}

type conflictingRiskEmails struct{ repository.EmailRepository }

func (c conflictingRiskEmails) SavePhishingResult(context.Context, domain.PhishingResult) error {
	return repository.ErrConflict
}
func TestPhishingAssessmentFailureIsNotBelowThreshold(t *testing.T) {
	s, _, _ := phishingOrganizationFixture(t)
	s.emails = conflictingRiskEmails{s.emails}
	list := s.Handle(context.Background(), OrganizationRequest{Operation: "list", Source: "phishing"})
	if list.Rows[0].Change != "risk_unavailable" || list.Rows[0].Phishing != nil {
		t.Fatal("failed assessment shown as low risk", list)
	}
}
