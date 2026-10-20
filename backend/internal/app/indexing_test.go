package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/model"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

type indexFake struct {
	mu         sync.Mutex
	inputs     int
	calls      int
	descriptor model.Descriptor
	invoke     func(context.Context, []string) ([][]float32, error)
}

func (f *indexFake) Descriptor() model.Descriptor { return f.descriptor }
func (f *indexFake) Close(context.Context) error  { return nil }
func (f *indexFake) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.inputs += len(texts)
	f.calls++
	invoke := f.invoke
	f.mu.Unlock()
	if invoke != nil {
		return invoke(ctx, texts)
	}
	return fakeIndexVectors(texts, f.descriptor.Dimensions), nil
}
func fakeIndexVectors(texts []string, dim int) [][]float32 {
	v := make([][]float32, len(texts))
	for i := range v {
		v[i] = make([]float32, dim)
		v[i][0] = 1
	}
	return v
}
func (f *indexFake) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.inputs }
func indexFixture(t *testing.T, n int) (*LocalIndex, *sqlite.Store, *indexFake, []domain.Email, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, a := range []domain.AccountID{"a", "b"} {
		if _, err = db.Accounts().Upsert(ctx, domain.Account{ID: a, Provider: "gmail", ProviderAccountID: string(a)}); err != nil {
			t.Fatal(err)
		}
	}
	emails := seedIndexEmails(t, db, n)
	f := &indexFake{descriptor: model.Identity()}
	s := newTestIndex(t, db, f)
	return s, db, f, emails, dir
}
func seedIndexEmails(t *testing.T, db *sqlite.Store, n int) []domain.Email {
	t.Helper()
	emails := make([]domain.Email, n)
	for i := range emails {
		emails[i] = domain.Email{ID: domain.EmailID(fmt.Sprintf("mail-%06d", i)), AccountID: "a", ProviderID: domain.ProviderMessageID(fmt.Sprintf("provider-%06d", i)), Subject: fmt.Sprintf("Project update %d", i), BodyText: "The project review is scheduled for Friday. Please review the attached agenda.", BodyState: "complete", ReceivedAt: time.Unix(1700000000+int64(i), 0)}
	}
	for off := 0; off < len(emails); off += 100 {
		if _, err := db.Emails().UpsertBatch(context.Background(), "a", emails[off:min(len(emails), off+100)]); err != nil {
			t.Fatal(err)
		}
	}
	return emails
}
func newTestIndex(t *testing.T, db *sqlite.Store, f localEmbedder) *LocalIndex {
	t.Helper()
	s, err := NewLocalIndex(context.Background(), func() (domain.AccountID, context.Context, error) { return "a", context.Background(), nil }, db.Emails(), db.Chunks(), db.Indexing(), f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func indexCommand(s *LocalIndex, op string) IndexResponse {
	return s.Manage(context.Background(), IndexRequest{Operation: op, AccountID: "a"})
}
func waitIndex(t *testing.T, s *LocalIndex) IndexResponse {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		p := indexCommand(s, "status")
		if p.Status != "running" {
			return p
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("index did not finish")
	return IndexResponse{}
}
func TestLocalIndexEntireCacheIdempotencyAndRebuild(t *testing.T) {
	s, db, f, emails, _ := indexFixture(t, 47)
	other := emails[0]
	other.ID = "other"
	other.AccountID = "b"
	if _, err := db.Emails().Upsert(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if p := indexCommand(s, "status"); p.EligibleMessages != 47 || p.PendingMessages != 47 || f.count() != 0 || p.Status != "idle" {
		t.Fatalf("not opt-in or wrong scope: %+v", p)
	}
	if p := s.Manage(context.Background(), IndexRequest{Operation: "start", AccountID: "b"}); p.Code != "unauthorized" {
		t.Fatal("cross-account start")
	}
	indexCommand(s, "start")
	p := waitIndex(t, s)
	if p.Status != "complete" || p.IndexedMessages != 47 || p.CompleteMessages != 47 || p.ChunksCreated != 94 || p.EmbeddingsGenerated != 94 || f.count() != 94 {
		t.Fatalf("not all cached messages indexed: %+v inputs=%d", p, f.count())
	}
	before, _ := db.Chunks().State(context.Background(), "a", emails[0].ID)
	calls := f.count()
	indexCommand(s, "start")
	p = waitIndex(t, s)
	after, _ := db.Chunks().State(context.Background(), "a", emails[0].ID)
	if f.count() != calls || before.Generation != after.Generation || p.Status != "complete" {
		t.Fatal("unchanged vectors regenerated")
	}
	indexCommand(s, "rebuild")
	p = waitIndex(t, s)
	if f.count() != calls || p.IndexedMessages != 47 {
		t.Fatal("rebuild regenerated valid cached vectors")
	}
	if _, err := db.Chunks().State(context.Background(), "b", other.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("unrelated account indexed")
	}
}
func TestLocalIndexPartialMissingAndFailedSources(t *testing.T) {
	s, db, _, emails, _ := indexFixture(t, 4)
	emails[1].BodyState = "unavailable"
	emails[1].BodyText = ""
	emails[2].BodyState = "unavailable"
	emails[2].BodyText = ""
	emails[2].Subject = ""
	emails[3].BodyText = strings.Repeat("bad ", 300000)
	for _, e := range emails {
		if _, err := db.Emails().Upsert(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	indexCommand(s, "start")
	p := waitIndex(t, s)
	if p.Status != "partial" || p.CompleteMessages != 1 || p.PartialMessages != 1 || p.FailedMessages != 1 || p.UnavailableMessages != 1 || p.PendingMessages != 0 || p.IndexedMessages != 2 {
		t.Fatalf("incorrect coverage: %+v", p)
	}
	emails[3].BodyText = "Fixed cached content."
	if _, err := db.Emails().Upsert(context.Background(), emails[3]); err != nil {
		t.Fatal(err)
	}
	indexCommand(s, "resume")
	p = waitIndex(t, s)
	if p.FailedMessages != 0 || p.CompleteMessages != 2 {
		t.Fatalf("failure not retried: %+v", p)
	}
}
func TestLocalIndexPauseCancelAndResumeWithinMessage(t *testing.T) {
	s, db, f, emails, _ := indexFixture(t, 1)
	emails[0].BodyText = strings.Repeat("A project meeting happens every Friday. ", 220)
	if _, err := db.Emails().Upsert(context.Background(), emails[0]); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	calls := 0
	f.invoke = func(ctx context.Context, texts []string) ([][]float32, error) {
		calls++
		if calls == 2 {
			close(blocked)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return fakeIndexVectors(texts, 384), nil
	}
	indexCommand(s, "start")
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("no second batch")
	}
	if p := indexCommand(s, "pause"); p.Status != "paused" || p.EmbeddingsGenerated != 4 || p.IndexedMessages != 0 || p.PartialMessages != 1 {
		t.Fatalf("partial batch claimed complete or pause lost: %+v", p)
	}
	if p := indexCommand(s, "cancel"); p.Status != "cancelled" {
		t.Fatal("cancel did not persist", p.Status)
	}
	// Join the stopped worker before changing the fake's behavior.
	<-s.worker.done
	f.mu.Lock()
	f.invoke = nil
	f.mu.Unlock()
	before := f.count()
	indexCommand(s, "resume")
	p := waitIndex(t, s)
	if p.Status != "complete" || p.EmbeddingsGenerated <= 4 || f.count()-before != p.EmbeddingsGenerated-4 {
		t.Fatalf("resume regenerated committed batch: %+v", p)
	}
}
func TestLocalIndexSourceChangeWhileInferenceIsInFlight(t *testing.T) {
	s, db, f, emails, _ := indexFixture(t, 2)
	entered, release := make(chan struct{}), make(chan struct{})
	once := sync.Once{}
	f.invoke = func(ctx context.Context, texts []string) ([][]float32, error) {
		once.Do(func() { close(entered); <-release })
		return fakeIndexVectors(texts, 384), nil
	}
	indexCommand(s, "start")
	<-entered
	emails[0].BodyText = "A replacement body with a different meeting schedule."
	if _, err := db.Emails().Upsert(context.Background(), emails[0]); err != nil {
		t.Fatal(err)
	}
	close(release)
	p := waitIndex(t, s)
	if p.Status != "complete" || p.IndexedMessages != 2 {
		t.Fatalf("stale work lost source: %+v", p)
	}
	chunks, err := db.Chunks().List(context.Background(), "a", emails[0].ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || !strings.Contains(chunks[1].Text, "replacement body") {
		t.Fatal("stale source published")
	}
	// A later change invalidates one message and reuses its unchanged subject chunk.
	before := f.count()
	emails[1].BodyText = "Only this second message changed."
	if _, err = db.Emails().Upsert(context.Background(), emails[1]); err != nil {
		t.Fatal(err)
	}
	if p = indexCommand(s, "status"); p.PendingMessages != 1 || p.IndexedMessages != 1 || p.Status != "pending" {
		t.Fatalf("invalidated unrelated state: %+v", p)
	}
	indexCommand(s, "start")
	p = waitIndex(t, s)
	if p.Status != "complete" || f.count() != before+1 {
		t.Fatal("unchanged chunks regenerated")
	}
}
func TestLocalIndexRestartAndModelGeneration(t *testing.T) {
	s, db, f, emails, dir := indexFixture(t, 3)
	indexCommand(s, "start")
	first := waitIndex(t, s)
	oldState, _ := db.Chunks().State(context.Background(), "a", emails[0].ID)
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate a process dying after recording a running job.
	run, err := db.Indexing().Begin(context.Background(), "a", s.profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	recovered := newTestIndex(t, reopened, f)
	p := indexCommand(recovered, "status")
	if p.Status != "interrupted" || p.Generation == run.Generation || f.count() != first.EmbeddingsGenerated {
		t.Fatalf("restart inferred or lost checkpoint: %+v", p)
	}
	indexCommand(recovered, "resume")
	p = waitIndex(t, recovered)
	if p.Status != "complete" || f.count() != first.EmbeddingsGenerated {
		t.Fatal("restart regenerated cached vectors")
	}
	_ = recovered.Close(context.Background())
	changed := &indexFake{descriptor: model.Identity()}
	changed.descriptor.Version += "-next"
	next := newTestIndex(t, reopened, changed)
	if p = indexCommand(next, "status"); p.Code != "model_changed" || p.PendingMessages != 3 || p.IndexedMessages != 0 {
		t.Fatalf("mixed incompatible generations: %+v", p)
	}
	indexCommand(next, "resume")
	p = waitIndex(t, next)
	if p.Status != "complete" || changed.count() != first.EmbeddingsGenerated || p.Generation == first.Generation {
		t.Fatal("model not rebuilt")
	}
	// The old versioned vector is retained, not interpreted as current.
	old, err := reopened.Chunks().Embedding(context.Background(), "a", emails[0].ID, "missing", oldState.Model)
	if err == nil || len(old.Vector) > 0 {
		t.Fatal("old model treated as current")
	}
}
func TestLocalIndexUnavailableAndInvalidVectorFailClosed(t *testing.T) {
	for _, failure := range []error{embedding.ErrUnavailable, embedding.ErrVector} {
		t.Run(failure.Error(), func(t *testing.T) {
			s, _, f, _, _ := indexFixture(t, 1)
			f.invoke = func(context.Context, []string) ([][]float32, error) { return nil, failure }
			indexCommand(s, "start")
			p := waitIndex(t, s)
			if p.Status != "failed" || p.IndexedMessages != 0 || p.EmbeddingsGenerated != 0 {
				t.Fatalf("invalid result published: %+v", p)
			}
		})
	}
}

func TestLocalIndexRestartPreservesAnIncompleteMessage(t *testing.T) {
	s, db, f, emails, dir := indexFixture(t, 1)
	emails[0].BodyText = strings.Repeat("A project meeting happens every Friday. ", 220)
	if _, err := db.Emails().Upsert(context.Background(), emails[0]); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	calls := 0
	f.invoke = func(ctx context.Context, texts []string) ([][]float32, error) {
		calls++
		if calls == 2 {
			close(blocked)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return fakeIndexVectors(texts, 384), nil
	}
	indexCommand(s, "start")
	<-blocked
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	fresh := &indexFake{descriptor: model.Identity()}
	resumed := newTestIndex(t, reopened, fresh)
	p := indexCommand(resumed, "status")
	if p.Status != "interrupted" || p.EmbeddingsGenerated != 4 || p.IndexedMessages != 0 || fresh.count() != 0 {
		t.Fatalf("restart lost partial checkpoint: %+v", p)
	}
	indexCommand(resumed, "resume")
	p = waitIndex(t, resumed)
	if p.Status != "complete" || fresh.count() != p.EmbeddingsGenerated-4 {
		t.Fatalf("restart regenerated durable batch: %+v", p)
	}
}

func TestLocalIndexRejectsMalformedBatchBeforeAnyVectorIsSaved(t *testing.T) {
	for _, kind := range []string{"shape", "dimension", "nonunit"} {
		t.Run(kind, func(t *testing.T) {
			s, _, f, _, _ := indexFixture(t, 1)
			f.invoke = func(_ context.Context, texts []string) ([][]float32, error) {
				vectors := fakeIndexVectors(texts, 384)
				switch kind {
				case "shape":
					return vectors[:1], nil
				case "dimension":
					vectors[1] = []float32{1}
				case "nonunit":
					vectors[1][0] = 2
				}
				return vectors, nil
			}
			indexCommand(s, "start")
			p := waitIndex(t, s)
			if p.Status != "failed" || p.EmbeddingsGenerated != 0 || p.IndexedMessages != 0 || p.FailedMessages != 1 {
				t.Fatalf("malformed batch published: %+v", p)
			}
		})
	}
}

func TestLocalIndexFinishedCoverageTracksNewUnavailableMailWithoutInference(t *testing.T) {
	s, db, f, _, _ := indexFixture(t, 1)
	indexCommand(s, "start")
	if p := waitIndex(t, s); p.Status != "complete" {
		t.Fatal(p.Status)
	}
	before := f.count()
	missing := domain.Email{ID: "new-unavailable", AccountID: "a", ProviderID: "unavailable-provider", BodyState: "unavailable", ReceivedAt: time.Unix(1700000000, 0)}
	if _, err := db.Emails().Upsert(context.Background(), missing); err != nil {
		t.Fatal(err)
	}
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	if err = chunker.Prepare(context.Background(), "a", missing.ProviderID); err != nil {
		t.Fatal(err)
	}
	p := indexCommand(s, "status")
	if p.Status != "partial" || p.UnavailableMessages != 1 || p.CompleteMessages != 1 || p.EligibleMessages != 2 || f.count() != before {
		t.Fatalf("finished index falsely claimed complete coverage: %+v", p)
	}
}

func TestLocalIndexReplacesOldPreprocessingAndEmbedsCleanText(t *testing.T) {
	s, db, f, emails, _ := indexFixture(t, 1)
	ctx := context.Background()
	e := emails[0]
	e.BodyText = "Double rewards October 5–16.\n[image: Example logo]\n<https://click.example.test/u/?qs=" + strings.Repeat("SyntheticTracking", 12) + ">\n[image: Save 14% at Northshop]\n[image: Save 14% at Northshop]\n> Unique quoted appointment at noon."
	if _, err := db.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Emails().Get(ctx, "a", e.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldProfile := s.profile
	oldProfile.PreprocessingVersion = "email-sections-v1-wp-07eced375cec-256-32-old"
	oldRun, err := db.Indexing().Begin(ctx, "a", oldProfile, false)
	if err != nil {
		t.Fatal(err)
	}
	chunker, err := mailtext.NewChunker()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := chunker.Build(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.PreprocessingVersion = oldProfile.PreprocessingVersion
	state, err := db.Indexing().ReplaceIndexed(ctx, oldRun, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := db.Chunks().List(ctx, "a", e.ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	batch := repository.EmbeddingBatch{State: state}
	for _, c := range chunks {
		v := make([]float32, 384)
		v[0] = 1
		batch.Embeddings = append(batch.Embeddings, repository.ChunkEmbedding{ChunkID: c.ID, Status: "ready", Vector: v})
	}
	if _, err = db.Indexing().SaveIndexed(ctx, oldRun, batch); err != nil {
		t.Fatal(err)
	}
	if err = db.Indexing().Transition(ctx, oldRun, "complete", ""); err != nil {
		t.Fatal(err)
	}
	p := indexCommand(s, "status")
	if p.Code != "preprocessing_changed" || p.IndexedMessages != 0 || p.PendingMessages != 1 || f.count() != 0 {
		t.Fatalf("old preprocessing counted as current or auto-inferred: %+v", p)
	}
	var inputs []string
	f.invoke = func(_ context.Context, texts []string) ([][]float32, error) {
		for _, text := range texts {
			if strings.Contains(text, "[image:") || strings.Contains(text, "SyntheticTracking") || strings.Contains(text, "?qs=") {
				return nil, embedding.ErrInput
			}
		}
		inputs = append(inputs, texts...)
		return fakeIndexVectors(texts, 384), nil
	}
	indexCommand(s, "resume")
	p = waitIndex(t, s)
	<-s.worker.done
	if p.Status != "complete" || p.IndexedMessages != 1 || p.EmbeddingsGenerated == 0 || f.count() != p.EmbeddingsGenerated {
		t.Fatalf("cleaning upgrade did not vectorize: %+v", p)
	}
	joined := strings.Join(inputs, "\n")
	if !strings.Contains(joined, "Save 14% at Northshop") || !strings.Contains(joined, "Unique quoted appointment at noon.") {
		t.Fatal("embedding input lost offer/quote facts")
	}
	after, err := db.Emails().Get(ctx, "a", e.ID)
	if err != nil || after.ContentHash != stored.ContentHash || after.Email.BodyText != e.BodyText {
		t.Fatal("indexing cleanup modified cached source")
	}
	count := f.count()
	indexCommand(s, "start")
	p = waitIndex(t, s)
	if p.Status != "complete" || f.count() != count {
		t.Fatal("unchanged cleaned text was re-embedded")
	}
}
