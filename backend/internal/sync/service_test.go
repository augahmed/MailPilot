package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

type readerStub struct {
	list  func(context.Context, provider.ListMessagesOptions) (provider.MessagePage, error)
	fetch func(context.Context, domain.ProviderMessageID) (domain.Email, error)
}

func (r readerStub) ListMessages(c context.Context, o provider.ListMessagesOptions) (provider.MessagePage, error) {
	return r.list(c, o)
}
func (r readerStub) FetchMessage(c context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	return r.fetch(c, id)
}
func finished(t *testing.T, s *Service) Progress {
	t.Helper()
	for deadline := time.Now().Add(time.Second * 3); time.Now().Before(deadline); {
		p := s.Status()
		if p.State != "running" {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker stuck")
	return Progress{}
}
func TestPartialFailureAndPaginationCycle(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.Accounts().Upsert(ctx, domain.Account{ID: "a", Provider: "fixture", ProviderAccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	r := readerStub{list: func(context.Context, provider.ListMessagesOptions) (provider.MessagePage, error) {
		return provider.MessagePage{Messages: []provider.MessageReference{{AccountID: a, ProviderID: "good"}, {AccountID: a, ProviderID: "bad"}}, NextPageToken: "cycle"}, nil
	}, fetch: func(_ context.Context, id domain.ProviderMessageID) (domain.Email, error) {
		if id == "bad" {
			return domain.Email{}, provider.ErrMalformed
		}
		return domain.Email{AccountID: a, ProviderID: id, ReceivedAt: time.Now(), BodyState: "complete"}, nil
	}}
	s := New(ctx, func() (domain.AccountID, context.Context, error) { return a, ctx, nil }, func(domain.AccountID) Reader { return r }, db.Emails(), 10)
	defer s.Clear()
	s.Start()
	p := finished(t, s)
	if p.State != "error" || p.Imported != 1 || p.Errors != 2 || p.Code != "pagination_error" {
		t.Fatalf("partial/cycle result: %+v", p)
	}
	rows, err := db.Emails().ListSummaries(ctx, a, repository.Page{Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("partial success lost", err)
	}
}
func TestSessionCancellationAndDisconnectedStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	stopped := make(chan struct{})
	r := readerStub{list: func(c context.Context, _ provider.ListMessagesOptions) (provider.MessagePage, error) {
		close(entered)
		<-c.Done()
		close(stopped)
		return provider.MessagePage{}, c.Err()
	}}
	s := New(context.Background(), func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, func(domain.AccountID) Reader { return r }, nil, 5)
	s.Start()
	<-entered
	cancel()
	<-stopped
	if p := finished(t, s); p.State != "cancelled" || p.Errors != 0 {
		t.Fatalf("cancellation: %+v", p)
	}
	s = New(context.Background(), func() (domain.AccountID, context.Context, error) { return "", nil, errors.New("disconnected") }, nil, nil, 5)
	if p := s.Start(); p.Code != "reauth_required" {
		t.Fatal("disconnected sync allowed")
	}
}
