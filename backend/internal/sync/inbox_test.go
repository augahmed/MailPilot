package sync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

func awaitInbox(t *testing.T, s *Service) Progress {
	t.Helper()
	s.Start()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Status()
		if p.State != "running" {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("bounded Inbox sync did not finish")
	return Progress{}
}

func TestInboxImports500Then500ThenRemainderAcrossRestart(t *testing.T) {
	db, s, r := deltaFixture(t, 500)
	ctx := context.Background()
	for i := 0; i < 1250; i++ {
		r.add(domain.ProviderMessageID(fmt.Sprintf("mail-%04d", i)))
	}
	// A legacy partial snapshot must repair the newest 500, including cached mail.
	for i := 0; i < 193; i++ {
		if _, err := db.Emails().Upsert(ctx, r.messages[r.refs[i].ProviderID]); err != nil {
			t.Fatal(err)
		}
	}
	old := repository.SyncCheckpoint{Cursor: "100", Phase: "snapshot"}
	if err := db.SyncCheckpoints().Commit(ctx, "a", repository.SyncCheckpoint{}, old, nil, ""); err != nil {
		t.Fatal(err)
	}
	for run, want := range []int{307, 500, 250} {
		if run == 1 {
			r.cursor = "110"
			r.records = []provider.ChangeRecord{{Cursor: "110", Changes: []provider.MessageChange{{ProviderID: "mail-0000"}}}}
		}
		p := awaitInbox(t, s)
		if p.State != "completed" || p.Imported != want || p.Errors != 0 {
			t.Fatalf("run=%d unexpected progress: %+v", run, p)
		}
		observations := 500
		if run == 1 {
			observations++
		}
		if p.Discovered != observations && run < 2 {
			t.Fatalf("run=%d did not finish 500 Inbox observations: %+v", run, p)
		}
		state, err := db.SyncCheckpoints().Load(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		if state.InboxComplete != (run == 2) || (run < 2 && !p.SnapshotLimited) {
			t.Fatal("lost Inbox continuation")
		}
		// Only SQLite checkpoints, not a prior worker's in-memory cursor, survive.
		s = New(ctx, s.session, s.factory, db.Emails(), 500)
		s.SetCheckpoints(db.SyncCheckpoints())
		t.Cleanup(s.Clear)
	}
	if p := awaitInbox(t, s); p.Discovered != 0 || p.Imported != 0 || p.More {
		t.Fatal("completed Inbox was needlessly scanned again", p)
	}
}

func TestInboxReadFailureDoesNotBlockLaterMessagesOrLoseRetry(t *testing.T) {
	db, s, r := deltaFixture(t, 500)
	ctx := context.Background()
	for i := 0; i < 600; i++ {
		r.add(domain.ProviderMessageID(fmt.Sprintf("mail-%04d", i)))
	}
	r.fail = "mail-0134"
	p := awaitInbox(t, s)
	if p.State != "completed" || p.Discovered != 500 || p.Imported != 499 || p.Errors != 1 || !p.More {
		t.Fatal("one failed read blocked the batch", p)
	}
	state, err := db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || len(state.RetryIDs) != 1 || state.RetryIDs[0] != r.fail {
		t.Fatal("failed read was lost", err)
	}
	if _, err := db.Emails().GetByProviderID(ctx, "a", "mail-0499"); err != nil {
		t.Fatal("later mail was not imported", err)
	}
	r.fail = ""
	restarted := New(ctx, s.session, s.factory, db.Emails(), 500)
	restarted.SetCheckpoints(db.SyncCheckpoints())
	defer restarted.Clear()
	p = awaitInbox(t, restarted)
	if p.State != "completed" || p.Imported != 101 || p.Errors != 0 {
		t.Fatal("retry or older continuation failed", p)
	}
	state, err = db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || len(state.RetryIDs) != 0 || !state.InboxComplete {
		t.Fatal("retry did not finish", err)
	}
}

func TestInboxCancellationResumesExactPendingPage(t *testing.T) {
	db, s, r := deltaFixture(t, 3)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		r.add(domain.ProviderMessageID(fmt.Sprintf("mail-%d", i)))
	}
	s.SetChunkProcessor(func(context.Context, domain.AccountID, domain.ProviderMessageID) error {
		s.Cancel()
		return context.Canceled
	})
	p := awaitInbox(t, s)
	if p.State != "cancelled" || p.Imported != 1 {
		t.Fatal("cancellation lost committed mail", p)
	}
	state, err := db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || len(state.InboxPending) != 7 || state.InboxPending[0] != "mail-1" {
		t.Fatal("pending page did not survive", err)
	}
	restarted := New(ctx, s.session, s.factory, db.Emails(), 3)
	restarted.SetCheckpoints(db.SyncCheckpoints())
	defer restarted.Clear()
	p = awaitInbox(t, restarted)
	if p.State != "completed" || p.Imported != 3 || p.Updated != 0 || r.lists != 1 {
		t.Fatal("restart re-listed or re-read completed entries", p, r.lists)
	}
}

func TestExpiredInboxPageTokenSkipsCachedPagesAndStillImportsOlderMail(t *testing.T) {
	db, s, r := deltaFixture(t, 500)
	ctx := context.Background()
	for i := 0; i < 550; i++ {
		r.add(domain.ProviderMessageID(fmt.Sprintf("mail-%04d", i)))
		if i < 500 {
			if _, err := db.Emails().Upsert(ctx, r.messages[r.refs[i].ProviderID]); err != nil {
				t.Fatal(err)
			}
		}
	}
	saved := repository.SyncCheckpoint{Phase: "history", Cursor: "100", InboxVersion: 1, InboxPageToken: "stale", Limited: true}
	if err := db.SyncCheckpoints().Commit(ctx, "a", repository.SyncCheckpoint{}, saved, nil, ""); err != nil {
		t.Fatal(err)
	}
	r.expiredToken = "stale"
	p := awaitInbox(t, s)
	if p.State != "completed" || p.Imported != 50 || p.Updated != 0 || p.Errors != 0 || p.Discovered != 50 {
		t.Fatal("token recovery re-consumed the newest 500", p)
	}
	state, err := db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || !state.InboxComplete {
		t.Fatal("older scan did not finish", err)
	}
}

func TestLegacyLimitedHistoryContinuesPastItsAlreadyCached500(t *testing.T) {
	db, s, r := deltaFixture(t, 500)
	ctx := context.Background()
	for i := 0; i < 550; i++ {
		r.add(domain.ProviderMessageID(fmt.Sprintf("mail-%04d", i)))
		if i < 500 {
			if _, err := db.Emails().Upsert(ctx, r.messages[r.refs[i].ProviderID]); err != nil {
				t.Fatal(err)
			}
		}
	}
	legacy := repository.SyncCheckpoint{Phase: "history", Cursor: "100", Limited: true}
	if err := db.SyncCheckpoints().Commit(ctx, "a", repository.SyncCheckpoint{}, legacy, nil, ""); err != nil {
		t.Fatal(err)
	}
	p := awaitInbox(t, s)
	if p.State != "completed" || p.Imported != 50 || p.Updated != 0 || p.Errors != 0 {
		t.Fatal("upgraded sync repeated its old initial subset", p)
	}
}
