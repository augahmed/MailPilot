package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func TestCategoryManagement(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a := account(t, s, "a")
	b := account(t, s, "b")
	defaults, err := s.Categories().List(ctx, a)
	if err != nil || len(defaults) != 6 {
		t.Fatalf("defaults: %d %v", len(defaults), err)
	}
	for _, c := range defaults {
		if len(c.Description) < 40 || !c.Enabled || c.CreatedAt.IsZero() || !c.CreatedAt.Equal(c.UpdatedAt) {
			t.Fatal("incomplete default")
		}
	}
	c := defaults[0]
	created := c.CreatedAt
	c.Name = "My finances"
	c.Description = "Bank statements and tax records, excluding purchase receipts."
	c.Enabled = false
	if _, err = s.Categories().Save(ctx, a, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Categories().Get(ctx, a, c.ID)
	if err != nil || got.Enabled || got.Description != c.Description || !got.CreatedAt.Equal(created) || !got.UpdatedAt.After(created) {
		t.Fatal("edit lost fields", err)
	}
	if err = s.Categories().Delete(ctx, b, c.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	email := saveEmail(t, s, sampleEmail(a, "message"))
	if err = s.Categories().Assign(ctx, a, email, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Categories().Delete(ctx, a, c.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("referenced delete", err)
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM categories WHERE id=?", c.ID); err == nil {
		t.Fatal("direct deletion bypassed references")
	}
	if err = s.Categories().Unassign(ctx, a, email, c.ID); err != nil {
		t.Fatal(err)
	}
	c.Enabled = true
	if _, err = s.Categories().Save(ctx, a, c); err != nil {
		t.Fatal(err)
	}
	if err = s.Classifications().Replace(ctx, assessment(t, s, a, email, c.ID)); err != nil {
		t.Fatal(err)
	}
	if err = s.Categories().Delete(ctx, a, c.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("analysis reference deleted", err)
	}
	free := defaults[1]
	if err = s.Categories().Delete(ctx, a, free.ID); err != nil {
		t.Fatal(err)
	}
	account(t, s, "a") // Reconnection/upsert must not restore a deleted default or overwrite edits.
	after, _ := s.Categories().List(ctx, a)
	if len(after) != 5 {
		t.Fatal("default recreated")
	}
	if _, err = s.Categories().Get(ctx, a, free.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal(err)
	}
	for _, bad := range []domain.EmailCategory{{Name: " ", Description: "Defined"}, {Name: "Name", Description: " "}, {Name: strings.Repeat("x", 101), Description: "Defined"}, {Name: "Name", Description: strings.Repeat("x", 1001)}} {
		if _, err = s.Categories().Save(ctx, a, bad); !errors.Is(err, repository.ErrInvalid) {
			t.Fatal("invalid accepted", err)
		}
	}
}
func TestCategoryReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := account(t, s, "a")
	id, err := s.Categories().Save(ctx, a, domain.EmailCategory{Name: "Travel", Description: "Travel reservations and itineraries.", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Categories().Get(ctx, a, id)
	if err != nil || c.Enabled || c.Description != "Travel reservations and itineraries." {
		t.Fatal("restart lost category", err)
	}
}

func TestUpgradeCategoriesPreservesLegacyReferences(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for i, entry := range entries[:3] {
		body, _ := migrations.ReadFile("migrations/" + entry.Name())
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", i+1, fmt.Sprintf("%x", sha256.Sum256(body))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO accounts VALUES('a','fixture','a','a@example.test','Fixture',NULL,1,1); INSERT INTO categories VALUES('legacy','a','School'); INSERT INTO categories VALUES('custom','a','Custom')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Categories().List(ctx, "a")
	if err != nil || len(rows) != 7 {
		t.Fatal("upgrade defaults", err, len(rows))
	}
	legacy, err := s.Categories().Get(ctx, "a", "legacy")
	if err != nil || legacy.Name != "School" || !legacy.Enabled || legacy.Description == "" || legacy.CreatedAt.UnixMilli() <= 0 {
		t.Fatal("legacy category lost", err)
	}
}
