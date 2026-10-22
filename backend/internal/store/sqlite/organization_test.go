package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func TestOrganizationJournal25EmailsAnd50Steps(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := account(t, store, "a")
	categories, _ := store.Categories().List(ctx, a)
	plan := domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta}
	for i := 0; i < 25; i++ {
		email := sampleEmail(a, fmt.Sprintf("remote-%d", i))
		email.ID = saveEmail(t, store, email)
		name := fmt.Sprintf("MailPilot/Category %d", i)
		plan.Rows = append(plan.Rows, domain.OrganizationRow{EmailID: email.ID, ProviderID: email.ProviderID, CategoryID: categories[0].ID, ProposedLabel: name})
		plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "create_label", Name: name, State: domain.ItemPending}, domain.OrganizationStep{Kind: "add", Name: name, EmailID: email.ID, State: domain.ItemPending})
	}
	saved, err := store.Organization().Create(ctx, plan)
	if err != nil || len(saved.Rows) != 25 || len(saved.Steps) != 50 {
		t.Fatal("25-email plan rejected", err)
	}
	restored, err := store.Organization().Get(ctx, a, saved.ID)
	if err != nil || restored.Digest != saved.Digest || len(restored.Steps) != 50 {
		t.Fatal("large plan lost immutable evidence", err)
	}
	tooManySteps := plan
	tooManySteps.Steps = append(append([]domain.OrganizationStep{}, plan.Steps...), plan.Steps[0])
	if _, err = store.Organization().Create(ctx, tooManySteps); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("51-step plan accepted", err)
	}
	plan.Rows = append(plan.Rows, plan.Rows[0])
	if _, err = store.Organization().Create(ctx, plan); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("26-email plan accepted", err)
	}
}

func TestOrganizationJournalRestartCASAndReferenceProtection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := account(t, s, "a")
	e := sampleEmail(a, "remote")
	e.ID = saveEmail(t, s, e)
	cats, _ := s.Categories().List(ctx, a)
	cat := cats[0]
	p, err := s.Organization().Create(ctx, domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{{EmailID: e.ID, ProviderID: e.ProviderID, CategoryID: cat.ID, ProposedLabel: "MailPilot/" + cat.Name}}, Steps: []domain.OrganizationStep{{Kind: "create_label", Name: "MailPilot/" + cat.Name, State: domain.ItemPending}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Categories().Delete(ctx, a, cat.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("deleted referenced category", err)
	}
	altered := p
	altered.Rows = append([]domain.OrganizationRow{}, p.Rows...)
	altered.Rows[0].ProposedLabel = "MailPilot/Changed"
	altered.Status = domain.ActionApplying
	if _, err := s.Organization().Update(ctx, altered); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("immutable preview edited")
	}
	p.Status = domain.ActionApplying
	p, err = s.Organization().Update(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Steps[0].State = domain.ItemInFlight
	p, err = s.Organization().Update(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	stale := p
	stale.Revision--
	if _, err := s.Organization().Update(ctx, stale); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale worker overwrote journal")
	}
	var confirmed int64
	if err := s.db.QueryRow(`SELECT confirmed_at FROM mailbox_actions WHERE id=?`, p.ID).Scan(&confirmed); err != nil || confirmed <= 0 {
		t.Fatal("missing parent confirmation", err)
	}
	_ = s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Organization().Latest(ctx, a, false)
	if err != nil || restored.Status != domain.ActionApplying || restored.Steps[0].State != domain.ItemInFlight || restored.Digest != p.Digest {
		t.Fatal("restart lost evidence", err)
	}
	restored.Steps[0].State = domain.ItemApplied
	restored.Steps[0].ResultID = "created"
	restored.Status = domain.ActionApplied
	restored, err = s.Organization().Update(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	var completed int64
	if err := s.db.QueryRow(`SELECT completed_at FROM mailbox_actions WHERE id=?`, p.ID).Scan(&completed); err != nil || completed < confirmed {
		t.Fatal("missing completion", err)
	}
	if _, err := s.Organization().Get(ctx, "other", p.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account journal leak")
	}
	expired, err := s.Organization().Create(ctx, domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta, Rows: p.Rows, Steps: []domain.OrganizationStep{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE mailbox_actions SET created_at=?,expires_at=? WHERE id=?`, time.Now().Add(-10*time.Minute).UnixMilli(), time.Now().Add(-time.Second).UnixMilli(), expired.ID); err != nil {
		t.Fatal(err)
	}
	// Expiration lives in the immutable plan, so simulate an old persisted preview
	// without rewriting it through the repository's protected mutation interface.
	expired.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	data, _ := encode(expired)
	_, _ = s.db.Exec(`UPDATE organization_plans SET plan_json=? WHERE action_id=?`, data, expired.ID)
	expired.Status = domain.ActionApplying
	if _, err := s.Organization().Update(ctx, expired); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("expired preview applied", err)
	}
}
