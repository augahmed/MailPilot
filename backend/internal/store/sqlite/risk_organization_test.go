package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func TestRiskOrganizationMigrationPreservesV6PlansAndIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for i, entry := range entries[:6] {
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", i+1, fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
			t.Fatal(err)
		}
	}
	old := &Store{db: db, version: 6}
	a := account(t, old, "a")
	b := account(t, old, "b")
	email := sampleEmail(a, "remote")
	email.ID = saveEmail(t, old, email)
	other := sampleEmail(b, "other")
	other.ID = saveEmail(t, old, other)
	cats, _ := old.Categories().List(ctx, a)
	plan, err := old.Organization().Create(ctx, domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{{EmailID: email.ID, ProviderID: email.ProviderID, CategoryID: cats[0].ID, ProposedLabel: "MailPilot/" + cats[0].Name}}, Steps: []domain.OrganizationStep{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Organization().Get(ctx, a, plan.ID)
	if err != nil || restored.Digest != plan.Digest || restored.Rows[0].CategoryID != cats[0].ID {
		t.Fatal("upgrade lost old plan", err)
	}
	riskPlan := domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{{EmailID: email.ID, ProviderID: email.ProviderID, InputHash: strings.Repeat("a", 64), ProposedLabel: domain.PotentialPhishingLabel, Phishing: &domain.OrganizationRisk{Score: 80, Threshold: 60, Version: "risk-test", Findings: []domain.PhishingFinding{{Code: "test", Points: 80, Explanation: "Synthetic risk"}}}}}, Steps: []domain.OrganizationStep{}}
	saved, err := s.Organization().Create(ctx, riskPlan)
	if err != nil {
		t.Fatal("category-free risk plan failed", err)
	}
	var category sql.NullString
	if err = s.db.QueryRow(`SELECT category_id FROM organization_emails WHERE action_id=?`, saved.ID).Scan(&category); err != nil || category.Valid {
		t.Fatal("risk invented category", err)
	}
	if _, err = s.db.Exec(`UPDATE organization_emails SET email_id=? WHERE action_id=?`, other.ID, saved.ID); err == nil {
		t.Fatal("lost account/email FK")
	}
	otherCats, _ := s.Categories().List(ctx, b)
	if _, err = s.db.Exec(`UPDATE organization_emails SET category_id=? WHERE action_id=?`, otherCats[0].ID, plan.ID); err == nil {
		t.Fatal("lost account/category FK")
	}
	if err = s.Categories().Delete(ctx, a, cats[0].ID); err == nil {
		t.Fatal("old plan category reference lost")
	}
	changed, err := s.Organization().Get(ctx, a, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed.Rows[0].Phishing.Score = 90
	changed.Status = domain.ActionApplying
	if _, err = s.Organization().Update(ctx, changed); err == nil {
		t.Fatal("mutable risk evidence")
	}
	riskPlan.Rows[0].Phishing.Score = 59
	if _, err = s.Organization().Create(ctx, riskPlan); err == nil {
		t.Fatal("below threshold plan persisted")
	}
}
