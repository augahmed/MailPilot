package sqlite

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"testing"
)

func TestSyncCheckpointAtomicityIsolationAndJournalSafeRemoval(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	email := sampleEmail(a, "remote")
	email.ID = saveEmail(t, s, email)
	foreign := sampleEmail(b, "remote")
	foreign.ID = saveEmail(t, s, foreign)
	cats, err := s.Categories().List(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.Organization().Create(ctx, domain.OrganizationPlan{AccountID: a, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{{EmailID: email.ID, ProviderID: email.ProviderID, CategoryID: cats[0].ID, ProposedLabel: "MailPilot/" + cats[0].Name}}, Steps: []domain.OrganizationStep{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO email_embeddings VALUES(?,?,?,2,?,?,?)`, a, email.ID, "fixture", "fixture", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	repo := s.SyncCheckpoints()
	empty := repository.SyncCheckpoint{}
	initial := repository.SyncCheckpoint{Phase: "history", Cursor: "100"}
	if err = repo.Commit(ctx, a, empty, initial, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err = repo.Commit(ctx, a, empty, initial, nil, "remote"); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale worker accepted")
	}
	next := initial
	next.Cursor = "200"
	if err = repo.Commit(ctx, a, initial, next, &foreign, ""); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("cross-account write accepted")
	}
	if got, _ := repo.Load(ctx, a); got != initial {
		t.Fatal("failed commit advanced checkpoint")
	}
	// An invalid nested label fails inside the transaction, after the row update.
	invalid := email
	invalid.Labels = []domain.EmailLabel{{ID: "not-the-existing-label", AccountID: a, ProviderID: email.Labels[0].ProviderID, Name: email.Labels[0].Name, Kind: email.Labels[0].Kind}}
	invalid.Subject = "must roll back"
	if err = repo.Commit(ctx, a, initial, next, &invalid, ""); err == nil {
		t.Fatal("expected nested label conflict")
	}
	unchanged, _ := s.Emails().Get(ctx, a, email.ID)
	if unchanged.Email.Subject != email.Subject {
		t.Fatal("failed commit retained partial mail update")
	}
	if err = repo.Commit(ctx, a, initial, next, nil, "remote"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Emails().Get(ctx, a, email.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("tombstone readable")
	}
	if _, err = s.Emails().Get(ctx, b, foreign.ID); err != nil {
		t.Fatal("other account deleted")
	}
	if restored, err := s.Organization().Get(ctx, a, plan.ID); err != nil || restored.Digest != plan.Digest {
		t.Fatal("journal damaged")
	}
	var body string
	var deleted, vectors int
	if err = s.db.QueryRow("SELECT body_text,remote_deleted FROM emails WHERE id=?", email.ID).Scan(&body, &deleted); err != nil || body != "" || deleted != 1 {
		t.Fatal("content not cleared")
	}
	s.db.QueryRow("SELECT count(*) FROM email_embeddings WHERE account_id=? AND email_id=?", a, email.ID).Scan(&vectors)
	if vectors != 0 {
		t.Fatal("deleted vector retained")
	}
	if other, _ := repo.Load(ctx, b); other != empty {
		t.Fatal("checkpoint leaked accounts")
	}
}
