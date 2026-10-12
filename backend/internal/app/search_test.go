package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/search"
)

type searchProvider struct {
	block   bool
	started chan struct{}
	once    sync.Once
}

func (p *searchProvider) ProviderName() string { return "fixture/search-model" }
func (p *searchProvider) Dimension() int       { return 2 }
func (p *searchProvider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	v, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (p *searchProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if p.block {
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.Contains(strings.ToLower(text), "receipt") {
			result[i] = []float32{1, 0}
		} else {
			result[i] = []float32{0, 1}
		}
	}
	return result, ctx.Err()
}
func searchFixture(t *testing.T, p *searchProvider) (*MailboxSearch, domain.Email) {
	t.Helper()
	store, review, receipt, _ := reviewFixture(t)
	e, err := embedding.NewLocalService(p, store.Emails(), store.Vectors())
	if err != nil {
		t.Fatal(err)
	}
	s := NewMailboxSearch(context.Background(), review.session, search.New(store.SearchIndex(), e, review.engine), store.Categories())
	t.Cleanup(s.Close)
	return s, receipt
}
func waitSearch(t *testing.T, s *MailboxSearch) SearchResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := s.Handle(context.Background(), SearchRequest{Operation: "status"})
		if r.State != "indexing" && r.State != "searching" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not finish")
	return SearchResponse{}
}
func TestSearchIndexAndQueryJobs(t *testing.T) {
	s, receipt := searchFixture(t, &searchProvider{})
	ctx := context.Background()
	before := s.Handle(ctx, SearchRequest{Operation: "status"})
	if before.AccountID != "a" || before.Counts.Indexed != 0 || before.Counts.Eligible != 2 {
		t.Fatal(before)
	}
	start := s.Handle(ctx, SearchRequest{Operation: "index", AccountID: "a"})
	if start.State != "indexing" || start.JobID == "" {
		t.Fatal(start)
	}
	indexed := waitSearch(t, s)
	if indexed.State != "completed" || indexed.Processed != 2 || indexed.Counts.Indexed != 2 || indexed.Errors != 0 {
		t.Fatal(indexed)
	}
	repeat := s.Handle(ctx, SearchRequest{Operation: "index", AccountID: "a"})
	if repeat.JobID == start.JobID {
		t.Fatal("job identity reused")
	}
	if r := waitSearch(t, s); r.Processed != 0 {
		t.Fatal("unchanged rows regenerated", r)
	}
	query := s.Handle(ctx, SearchRequest{Operation: "query", AccountID: "a", Query: "payment receipt", TopK: 1})
	if query.State != "searching" || len(query.Rows) != 0 {
		t.Fatal(query)
	}
	result := waitSearch(t, s)
	if result.State != "completed" || len(result.Rows) != 1 || result.Rows[0].EmailID != string(receipt.ID) || result.Rows[0].Excerpt == "" {
		t.Fatal(result)
	}
	for _, request := range []SearchRequest{{Operation: "query", AccountID: "other", Query: "receipt", TopK: 2}, {Operation: "cancel", AccountID: "a", JobID: "old-job"}, {Operation: "query", AccountID: "a", Query: "receipt", TopK: 0}, {Operation: "status", Query: "not allowed"}} {
		r := s.Handle(ctx, request)
		if r.Code == "" || len(r.Rows) != 0 {
			t.Fatal("invalid/cross-account request accepted", r)
		}
	}
	// A rejected cross-account request does not clear the valid account's result.
	if r := s.Handle(ctx, SearchRequest{Operation: "status"}); len(r.Rows) != 1 {
		t.Fatal("cross-account request cleared authorized results", r)
	}
}
func TestSearchCancellationDisconnectAndClose(t *testing.T) {
	p := &searchProvider{block: true, started: make(chan struct{})}
	s, _ := searchFixture(t, p)
	ctx := context.Background()
	start := s.Handle(ctx, SearchRequest{Operation: "index", AccountID: "a"})
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	r := s.Handle(ctx, SearchRequest{Operation: "cancel", AccountID: "a", JobID: start.JobID})
	if r.JobID != start.JobID {
		t.Fatal(r)
	}
	done := waitSearch(t, s)
	if done.State != "cancelled" || len(done.Rows) != 0 {
		t.Fatal(done)
	}
	start = s.Handle(ctx, SearchRequest{Operation: "index", AccountID: "a"})
	s.Clear()
	s.Close()
	s.session = func() (domain.AccountID, context.Context, error) { return "", nil, errors.New("unauthorized") }
	if r := s.Handle(ctx, SearchRequest{Operation: "status"}); r.Code != "unauthorized" || r.AccountID != "" || len(r.Rows) != 0 {
		t.Fatal("disconnect leaked results", r)
	}
}
func TestSearchUnavailableAndFrameLimits(t *testing.T) {
	store, review, _, _ := reviewFixture(t)
	s := NewMailboxSearch(context.Background(), review.session, search.New(store.SearchIndex(), nil, review.engine), store.Categories())
	defer s.Close()
	r := s.Handle(context.Background(), SearchRequest{Operation: "query", AccountID: "a", Query: "receipt", TopK: 5})
	if r.Code != "embedding_unavailable" || r.Counts.Cached != 2 || len(r.Rows) != 0 {
		t.Fatal(r)
	}
	r = emptySearch()
	r.AccountID = "a"
	r.JobID = "job"
	r.Operation = "query"
	r.State = "completed"
	for i := 0; i < 20; i++ {
		r.Rows = append(r.Rows, SearchHitDTO{EmailID: strings.Repeat("x", 100), Sender: strings.Repeat("😀", 81), Subject: strings.Repeat("😀", 121), Excerpt: strings.Repeat("😀", 161)})
	}
	data, err := json.Marshal(boundedSearch(r))
	if err != nil || len(data) > 48*1024 {
		t.Fatal("private frame overflow", len(data), err)
	}
}
