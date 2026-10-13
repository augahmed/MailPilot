package sync

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
	"strconv"
	"testing"
	"time"
)

type deltaStub struct {
	messages              map[domain.ProviderMessageID]domain.Email
	refs                  []provider.MessageReference
	records               []provider.ChangeRecord
	cursor                string
	fail                  domain.ProviderMessageID
	expired               bool
	lists, full, metadata int
	block                 func(context.Context)
}

func (r *deltaStub) CurrentCheckpoint(context.Context) (string, error) { return r.cursor, nil }
func (r *deltaStub) ListMessages(_ context.Context, o provider.ListMessagesOptions) (provider.MessagePage, error) {
	r.lists++
	offset, _ := strconv.Atoi(o.Page.PageToken)
	end := min(offset+o.Page.Limit, len(r.refs))
	next := ""
	if end < len(r.refs) {
		next = strconv.Itoa(end)
	}
	return provider.MessagePage{Messages: r.refs[offset:end], NextPageToken: next}, nil
}
func (r *deltaStub) FetchMessage(_ context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	r.full++
	m, ok := r.messages[id]
	if !ok {
		return m, provider.ErrNotFound
	}
	return m, nil
}
func (r *deltaStub) FetchMessageMetadata(ctx context.Context, id domain.ProviderMessageID) (provider.MessageMetadata, error) {
	r.metadata++
	if r.block != nil {
		r.block(ctx)
		return provider.MessageMetadata{}, ctx.Err()
	}
	if id == r.fail {
		return provider.MessageMetadata{}, provider.ErrUnavailable
	}
	m, ok := r.messages[id]
	if !ok {
		return provider.MessageMetadata{}, provider.ErrNotFound
	}
	inScope := false
	for _, l := range m.Labels {
		if l.ProviderID == "inbox" {
			inScope = true
		}
	}
	return provider.MessageMetadata{ProviderID: id, ProviderThreadID: m.ProviderThreadID, ContentRevision: m.ContentRevision, Labels: m.Labels, InSyncScope: inScope}, nil
}
func (r *deltaStub) ListChanges(_ context.Context, cursor string, _ provider.PageOptions) (provider.ChangePage, error) {
	if r.expired {
		r.expired = false
		return provider.ChangePage{}, provider.ErrCheckpointExpired
	}
	n, _ := strconv.Atoi(cursor)
	page := provider.ChangePage{Cursor: r.cursor}
	for _, record := range r.records {
		id, _ := strconv.Atoi(record.Cursor)
		if id > n {
			page.Records = append(page.Records, record)
		}
	}
	return page, nil
}
func deltaFixture(t *testing.T, limit int) (*sqlite.Store, *Service, *deltaStub) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "fixture", ProviderAccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	r := &deltaStub{messages: map[domain.ProviderMessageID]domain.Email{}, cursor: "100"}
	s := New(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, func(domain.AccountID) Reader { return r }, db.Emails(), limit)
	s.SetCheckpoints(db.SyncCheckpoints())
	t.Cleanup(s.Clear)
	return db, s, r
}
func (r *deltaStub) add(id domain.ProviderMessageID) {
	r.messages[id] = domain.Email{AccountID: "a", ProviderID: id, ProviderThreadID: "thread", ContentRevision: string(id), Subject: "cached", BodyText: "preserve this body", BodyState: "complete", ReceivedAt: time.Unix(1, 0), Labels: []domain.EmailLabel{{AccountID: "a", ProviderID: "inbox", Name: "Inbox", Kind: domain.LabelKindSystem}}}
	r.refs = append(r.refs, provider.MessageReference{AccountID: "a", ProviderID: id})
}
func runDelta(t *testing.T, s *Service) Progress {
	t.Helper()
	s.Start()
	p := finished(t, s)
	if p.State != "completed" {
		t.Fatalf("sync failed: %+v", p)
	}
	return p
}
func TestInitialIncrementalReplayLabelsAndRemovals(t *testing.T) {
	db, s, r := deltaFixture(t, 10)
	ctx := context.Background()
	r.add("one")
	p := runDelta(t, s)
	if p.Mode != "initial" || p.Imported != 1 || r.full != 1 {
		t.Fatalf("initial: %+v", p)
	}
	first, _ := db.Emails().GetByProviderID(ctx, "a", "one")
	r.add("two")
	m := r.messages["one"]
	m.Labels = nil
	m.ProviderThreadID = "updated-thread"
	r.messages["one"] = m
	r.cursor = "120"
	r.records = []provider.ChangeRecord{{Cursor: "110", Changes: []provider.MessageChange{{ProviderID: "one"}, {ProviderID: "two"}}}}
	p = runDelta(t, s)
	if p.Mode != "incremental" || p.Imported != 1 || p.Updated != 1 || r.full != 2 || r.lists != 1 {
		t.Fatalf("incremental: %+v, full=%d lists=%d", p, r.full, r.lists)
	}
	after, _ := db.Emails().GetByProviderID(ctx, "a", "one")
	if after.Email.ID != first.Email.ID || after.Email.BodyText != first.Email.BodyText || len(after.Email.Labels) != 0 || after.Email.ProviderThreadID != "updated-thread" {
		t.Fatal("metadata update damaged content")
	}
	calls := r.metadata
	p = runDelta(t, s)
	if p.Discovered != 0 || r.metadata != calls {
		t.Fatal("repeat refetched unchanged messages")
	}
	r.cursor = "130"
	r.records = append(r.records, provider.ChangeRecord{Cursor: "130", Changes: []provider.MessageChange{{ProviderID: "one", Deleted: true}}})
	p = runDelta(t, s)
	if p.Removed != 1 {
		t.Fatal("deletion not reported")
	}
	if _, err := db.Emails().Get(ctx, "a", first.Email.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("deleted email still readable")
	}
	rows, _ := db.Emails().ListSummaries(ctx, "a", repository.Page{Limit: 10})
	if len(rows) != 1 {
		t.Fatal("deleted mail still listed")
	}
}
func TestPartialHistoryRecordResumesAndFailureDoesNotSkip(t *testing.T) {
	db, s, r := deltaFixture(t, 1)
	ctx := context.Background()
	runDelta(t, s)
	r.add("one")
	r.add("two")
	r.cursor = "200"
	r.records = []provider.ChangeRecord{{Cursor: "200", Changes: []provider.MessageChange{{ProviderID: "one"}, {ProviderID: "two"}}}}
	p := runDelta(t, s)
	state, _ := db.SyncCheckpoints().Load(ctx, "a")
	if !p.More || state.Cursor != "100" || state.Record != "200" || state.Offset != 1 {
		t.Fatalf("partial checkpoint: %+v", state)
	}
	r.fail = "two"
	s.Start()
	p = finished(t, s)
	if p.State != "error" || p.Errors != 1 {
		t.Fatal("failure not reported")
	}
	unchanged, _ := db.SyncCheckpoints().Load(ctx, "a")
	if unchanged != state {
		t.Fatal("skipped failure")
	}
	r.fail = ""
	runDelta(t, s)
	state, _ = db.SyncCheckpoints().Load(ctx, "a")
	if state.Cursor != "200" || state.Record != "" {
		t.Fatal("record did not finish")
	}
	rows, _ := db.Emails().ListSummaries(ctx, "a", repository.Page{Limit: 10})
	if len(rows) != 2 {
		t.Fatal("duplicates or lost messages")
	}
}
func TestExpiredCheckpointRecoveryAndCancellation(t *testing.T) {
	db, s, r := deltaFixture(t, 2)
	ctx := context.Background()
	r.add("one")
	r.add("two")
	runDelta(t, s)
	delete(r.messages, "one")
	r.refs = nil
	r.add("three")
	r.cursor = "500"
	r.expired = true
	if p := runDelta(t, s); p.Mode != "recovery" || !p.More {
		t.Fatal("expiration not exposed")
	}
	runDelta(t, s)
	runDelta(t, s)
	runDelta(t, s)
	runDelta(t, s)
	if _, err := db.Emails().GetByProviderID(ctx, "a", "one"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("confirmed missing cached mail retained")
	}
	if _, err := db.Emails().GetByProviderID(ctx, "a", "two"); err != nil {
		t.Fatal("absence from listing incorrectly deleted cache")
	}
	if _, err := db.Emails().GetByProviderID(ctx, "a", "three"); err != nil {
		t.Fatal("new snapshot mail missing")
	}
	state, _ := db.SyncCheckpoints().Load(ctx, "a")
	if state.Phase != "history" || state.Cursor != "500" {
		t.Fatalf("recovery did not finish: %+v", state)
	}
	entered := make(chan struct{})
	r.block = func(ctx context.Context) { close(entered); <-ctx.Done() }
	r.cursor = "600"
	r.records = []provider.ChangeRecord{{Cursor: "600", Changes: []provider.MessageChange{{ProviderID: "two"}}}}
	s.Start()
	<-entered
	s.Cancel()
	if p := finished(t, s); p.State != "cancelled" {
		t.Fatal("cancellation not reported")
	}
	after, _ := db.SyncCheckpoints().Load(ctx, "a")
	if after != state {
		t.Fatal("cancelled read advanced checkpoint")
	}
}

func TestInitialPartialFailureRetainsAnchorAndRestartsIdempotently(t *testing.T) {
	db, s, r := deltaFixture(t, 10)
	ctx := context.Background()
	r.add("one")
	r.add("two")
	r.fail = "two"
	s.Start()
	p := finished(t, s)
	if p.State != "error" || p.Imported != 1 || p.Errors != 1 {
		t.Fatalf("partial initial: %+v", p)
	}
	state, _ := db.SyncCheckpoints().Load(ctx, "a")
	if state.Cursor != "100" || state.Phase != "snapshot" {
		t.Fatal("partial initial advanced baseline")
	}
	first, _ := db.Emails().GetByProviderID(ctx, "a", "one")
	r.fail = ""
	r.cursor = "200"
	// A new Service simulates restart; durable state, not the old worker, resumes.
	restarted := New(ctx, s.session, s.factory, db.Emails(), 10)
	restarted.SetCheckpoints(db.SyncCheckpoints())
	defer restarted.Clear()
	runDelta(t, restarted)
	after, _ := db.Emails().GetByProviderID(ctx, "a", "one")
	if first.Email.ID != after.Email.ID {
		t.Fatal("reimport duplicated message")
	}
	state, _ = db.SyncCheckpoints().Load(ctx, "a")
	if state.Cursor != "100" || state.Phase != "history" {
		t.Fatal("lost pre-import anchor")
	}
	r.records = []provider.ChangeRecord{{Cursor: "150", Changes: []provider.MessageChange{{ProviderID: "two"}}}}
	runDelta(t, restarted)
	state, _ = db.SyncCheckpoints().Load(ctx, "a")
	if state.Cursor != "200" {
		t.Fatal("did not catch changes during initial import")
	}
}
