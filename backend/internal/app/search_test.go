package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

func (f *indexFake) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	v, err := f.EmbedDocument(ctx, []string{q})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func searchRequest(op string) SearchRequest { return SearchRequest{Operation: op, AccountID: "a"} }

func TestDemoQueryDoesNotInitializeInferenceBeforeIndexStart(t *testing.T) {
	idx, db, f, emails, _ := indexFixture(t, 2)
	ctx := context.Background()
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if err = chunker.Prepare(ctx, "a", email.ProviderID); err != nil {
			t.Fatal(err)
		}
	}
	s := NewLocalSearch(ctx, idx.session, retrieval.New(db.Vectors(), f), db.Indexing())
	s.RequireIndexStart()
	defer s.Close(ctx)
	s.Manage(ctx, SearchRequest{Operation: "start", AccountID: "a", Query: "project", Limit: 10, Mode: "hybrid"})
	r := waitSearch(t, s)
	if r.Status != "complete" || r.Mode != "keyword" || r.Warning != "semantic_unavailable" || len(r.Emails) == 0 || f.count() != 0 {
		t.Fatal("demo query inferred before explicit indexing", r.Status, r.Code)
	}
	if r = s.Manage(ctx, SearchRequest{Operation: "start", AccountID: "a", Query: "project", Limit: 10, Mode: "semantic"}); r.Code != "model_unavailable" || f.count() != 0 {
		t.Fatal("semantic request bypassed demo enablement")
	}
	indexCommand(idx, "start")
	if r := waitIndex(t, idx); r.Status != "complete" {
		t.Fatal(r.Code)
	}
	count := f.count()
	s.Manage(ctx, SearchRequest{Operation: "start", AccountID: "a", Query: "project", Limit: 10, Mode: "hybrid"})
	r = waitSearch(t, s)
	if r.Status != "complete" || r.Mode != "hybrid" || f.count() <= count {
		t.Fatal("explicitly enabled demo query did not reuse local model", r.Code)
	}
}
func waitSearch(t *testing.T, s *LocalSearch) SearchResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := s.Manage(context.Background(), searchRequest("status"))
		if r.Status != "running" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("search timeout")
	return SearchResponse{}
}
func TestLocalSearchAuthorizationFreshnessAndDisabledAI(t *testing.T) {
	idx, db, f, emails, _ := indexFixture(t, 3)
	indexCommand(idx, "start")
	if got := waitIndex(t, idx); got.Status != "complete" {
		t.Fatal(got)
	}
	ctx := context.Background()
	session, cancel := context.WithCancel(ctx)
	defer cancel()
	s := NewLocalSearch(ctx, func() (domain.AccountID, context.Context, error) { return "a", session, nil }, retrieval.New(db.Vectors(), f), db.Indexing())
	defer s.Close(ctx)
	count := f.count()
	r := s.Manage(ctx, searchRequest("status"))
	if r.AccountID != "a" || r.SearchableMessages != 3 || f.count() != count {
		t.Fatal("status inferred", r)
	}
	wrong := SearchRequest{Operation: "start", AccountID: "b", Query: "meeting", Limit: 10}
	if got := s.Manage(ctx, wrong); got.Code != "unauthorized" || f.count() != count {
		t.Fatal("cross-account inference", got)
	}
	request := SearchRequest{Operation: "start", AccountID: "a", Query: "meeting", Limit: 10}
	s.Manage(ctx, request)
	got := waitSearch(t, s)
	if got.Status != "complete" || len(got.Emails) != 3 {
		t.Fatalf("search %+v", got)
	}
	// No AI settings, adapters or keys exist in this fixture.
	if got.Emails[0].EmailID != "mail-000000" {
		t.Fatal("unstable email tie", got)
	}
	emails[0].BodyText += " revised"
	if _, err := db.Emails().Upsert(ctx, emails[0]); err != nil {
		t.Fatal(err)
	}
	got = s.Manage(ctx, searchRequest("status"))
	if got.Code != "stale" || len(got.Emails) != 0 {
		t.Fatal("stale results exposed", got)
	}
	s.Manage(ctx, request)
	got = waitSearch(t, s)
	if len(got.Emails) != 2 {
		t.Fatal("stale source returned", got)
	}
	cancel()
	got = s.Manage(ctx, searchRequest("status"))
	if got.Code != "unauthorized" || len(got.Emails) != 0 || got.AccountID != "" {
		t.Fatal("revoked session exposed mail", got)
	}
}
func TestLocalSearchCancelUnavailableAndStaleJobs(t *testing.T) {
	idx, db, f, _, _ := indexFixture(t, 1)
	ctx := context.Background()
	s := NewLocalSearch(ctx, idx.session, retrieval.New(db.Vectors(), f), db.Indexing())
	defer s.Close(ctx)
	entered := make(chan struct{})
	f.invoke = func(ctx context.Context, texts []string) ([][]float32, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	request := SearchRequest{Operation: "start", AccountID: "a", Query: "meeting", Limit: 5}
	started := s.Manage(ctx, request)
	<-entered
	if got := s.Manage(ctx, request); got.Code != "busy" {
		t.Fatal("unbounded concurrent searches")
	}
	cancel := searchRequest("cancel")
	cancel.JobID = strings.Repeat("a", 32)
	if got := s.Manage(ctx, cancel); got.Code != "stale" {
		t.Fatal("wrong job cancelled current")
	}
	cancel.JobID = started.JobID
	got := s.Manage(ctx, cancel)
	if got.Status != "cancelled" || len(got.Emails) != 0 {
		t.Fatal("cancel failed", got)
	}
	<-s.job.done
	f.mu.Lock()
	f.invoke = func(context.Context, []string) ([][]float32, error) {
		return nil, errors.New("private provider diagnostics")
	}
	f.mu.Unlock()
	s.Manage(ctx, request)
	got = waitSearch(t, s)
	if got.Code != "storage_error" || len(got.Emails) != 0 {
		t.Fatal("unsafe error", got)
	}
	<-s.job.done
	f.mu.Lock()
	f.invoke = func(context.Context, []string) ([][]float32, error) { return nil, embedding.ErrUnavailable }
	f.mu.Unlock()
	s.Manage(ctx, request)
	got = waitSearch(t, s)
	if got.Code != "model_unavailable" || len(got.Emails) != 0 {
		t.Fatal("unavailable model did not fail closed", got)
	}
	s.Clear()
	if got = s.Manage(ctx, searchRequest("status")); len(got.Emails) != 0 {
		t.Fatal("clear exposed results")
	}
}
func TestLocalSearchBoundedValidation(t *testing.T) {
	for _, r := range []SearchRequest{{Operation: "start", AccountID: "a", Query: "find", Limit: 21}, {Operation: "start", AccountID: "a", Query: "", Limit: 5}, {Operation: "status", Query: "find"}, {Operation: "cancel", AccountID: "a"}, {Operation: "status", Filter: repository.SearchFilter{Sender: "foo"}}} {
		if r.Valid() {
			t.Fatal("invalid request accepted", r)
		}
	}
}

func TestKeywordSearchNeedsNoInferenceAndHybridDisclosesFallback(t *testing.T) {
	_, db, f, emails, _ := indexFixture(t, 2)
	ctx := context.Background()
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if err = chunker.Prepare(ctx, "a", email.ProviderID); err != nil {
			t.Fatal(err)
		}
	}
	f.invoke = func(context.Context, []string) ([][]float32, error) { return nil, embedding.ErrUnavailable }
	s := NewLocalSearch(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, retrieval.New(db.Vectors(), f), db.Indexing())
	defer s.Close(ctx)
	for _, mode := range []string{"keyword", "hybrid"} {
		s.Manage(ctx, SearchRequest{Operation: "start", AccountID: "a", Query: "project review", Limit: 10, Mode: mode})
		got := waitSearch(t, s)
		if got.Status != "complete" || got.Mode != "keyword" || got.RequestedMode != mode || got.KeywordMessages != 2 || got.SearchableMessages != 0 || len(got.Emails) != 2 {
			t.Fatal(got)
		}
		if mode == "keyword" && f.count() != 0 {
			t.Fatal("keyword search invoked inference")
		}
		if mode == "hybrid" && got.Warning != "semantic_unavailable" {
			t.Fatal("silent fallback", got)
		}
		count := f.count()
		s.Manage(ctx, searchRequest("status"))
		if f.count() != count {
			t.Fatal("polling invoked inference")
		}
	}
}

func TestLocalSearchRevokedWorkerCannotPublishAndShutdownJoins(t *testing.T) {
	_, db, f, _, _ := indexFixture(t, 1)
	ctx := context.Background()
	old, cancel := context.WithCancel(ctx)
	session := old
	entered, release := make(chan struct{}), make(chan struct{})
	f.invoke = func(context.Context, []string) ([][]float32, error) {
		close(entered)
		<-release
		return fakeIndexVectors([]string{"q"}, f.descriptor.Dimensions), nil
	}
	s := NewLocalSearch(ctx, func() (domain.AccountID, context.Context, error) { return "a", session, nil }, retrieval.New(db.Vectors(), f), db.Indexing())
	s.Manage(ctx, SearchRequest{Operation: "start", AccountID: "a", Query: "find", Limit: 5})
	<-entered
	cancel()
	session = context.Background()
	if got := s.Manage(ctx, searchRequest("status")); got.Code != "busy" || len(got.Emails) != 0 {
		t.Fatal("session replacement lost active worker", got)
	}
	closeCtx, closeCancel := context.WithCancel(ctx)
	closeCancel()
	if err := s.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown failed to join old worker", err)
	}
	close(release)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.Manage(ctx, searchRequest("status")); len(got.Emails) != 0 || got.Code != "unavailable" {
		t.Fatal("late revoked result", got)
	}
}
