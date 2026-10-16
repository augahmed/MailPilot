package sqlite

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"strings"
	"testing"
)

// Existing databases can retain historical vectors without a runtime or rebuild.
// Content changes invalidate only affected derived rows, never cached mail.
func TestHistoricalIndexesStayInertAcrossReopenAndContentUpdates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := account(t, s, "history-a")
	b := account(t, s, "history-b")
	ea := sampleEmail(a, "one")
	eb := sampleEmail(b, "two")
	ea.ID = saveEmail(t, s, ea)
	eb.ID = saveEmail(t, s, eb)
	tables := []string{"email_embeddings", "email_passage_vectors", "email_retrieval_indexes"}
	for _, e := range []domain.Email{ea, eb} {
		for _, table := range tables {
			values := "?,?,?,?,?,?,?"
			args := []any{e.AccountID, e.ID, "historical-model", 2, "historical-v1", strings.Repeat("a", 64), make([]byte, 8)}
			if table == "email_retrieval_indexes" {
				values = "?,?,?,?,?,?"
				args = args[:6]
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO "+table+" VALUES("+values+")", args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(wantA, wantB int) {
		t.Helper()
		for _, table := range tables {
			for _, tc := range []struct {
				account domain.AccountID
				want    int
			}{{a, wantA}, {b, wantB}} {
				var got int
				if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE account_id=?", tc.account).Scan(&got); err != nil || got != tc.want {
					t.Fatalf("%s: %d %v", table, got, err)
				}
			}
		}
	}
	check(1, 1)
	saveEmail(t, s, ea) // Reimport of identical content does not purge historical evidence.
	check(1, 1)
	ea.BodyText = "Updated synthetic content"
	saveEmail(t, s, ea)
	check(0, 1)
	for _, e := range []domain.Email{ea, eb} {
		got, err := s.Emails().Get(ctx, e.AccountID, e.ID)
		if err != nil || got.Email.BodyText != e.BodyText {
			t.Fatal("cached mail lost", err)
		}
	}
}
