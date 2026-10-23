package sqlite

import (
	"context"
	"testing"
)

func TestClassificationScopeMigrationPreservesMailAndScopesIndexRevisions(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 20)
	a := account(t, old, "classification-scope-upgrade")
	first, input := chunkFixture(t, old, a, "first")
	replaceChunks(t, old, input)
	second, other := chunkFixture(t, old, a, "second")
	replaceChunks(t, old, other)
	read := func(s *Store) (string, string, string) {
		t.Helper()
		var shared, one, two string
		if err := s.db.QueryRow(`SELECT revision FROM classification_state WHERE account_id=?`, a).Scan(&shared); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT revision FROM classification_email_state WHERE account_id=? AND email_id=?`, a, first.ID).Scan(&one); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT revision FROM classification_email_state WHERE account_id=? AND email_id=?`, a, second.ID).Scan(&two); err != nil {
			t.Fatal(err)
		}
		return shared, one, two
	}
	shared, one, two := read(old)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, firstRev, secondRev := read(s)
	if after != shared || firstRev != one || secondRev != two {
		t.Fatal("upgrade changed existing assessment identities")
	}
	if _, err := s.db.Exec(`UPDATE index_state SET generation=lower(hex(randomblob(16))) WHERE account_id=? AND email_id=?`, a, second.ID); err != nil {
		t.Fatal(err)
	}
	after, firstRev, secondRev = read(s)
	if after != shared || firstRev != one || secondRev == two {
		t.Fatal("index change was not scoped to its email")
	}
	if _, err := s.Emails().Get(ctx, a, first.ID); err != nil {
		t.Fatal("upgrade lost source", err)
	}
}
