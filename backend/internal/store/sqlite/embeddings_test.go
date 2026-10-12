package sqlite

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

// Synthetic vectors exercise adapter substitution, not semantic quality.
type fixtureEmbedder struct {
	name       string
	dimension  int
	calls      int
	batchSizes []int
	fail       error
	output     [][]float32
	hook       func()
}

func (p *fixtureEmbedder) ProviderName() string {
	if p.name == "" {
		return "fixture/model-v1"
	}
	return p.name
}
func (p *fixtureEmbedder) Dimension() int {
	if p.dimension == 0 {
		return 2
	}
	return p.dimension
}
func (p *fixtureEmbedder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	v, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, nil
	}
	return v[0], nil
}
func (p *fixtureEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	p.calls++
	p.batchSizes = append(p.batchSizes, len(texts))
	if p.hook != nil {
		p.hook()
	}
	if p.fail != nil {
		return nil, p.fail
	}
	if p.output != nil {
		return p.output, nil
	}
	v := make([][]float32, len(texts))
	for i, text := range texts {
		v[i] = []float32{float32(len(text)), 1}
	}
	return v, ctx.Err()
}
func embeddingService(t *testing.T, s *Store, p embedding.EmbeddingProvider) *embedding.Service {
	t.Helper()
	service, err := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func vectorRecord(t *testing.T, s *Store, a domain.AccountID, id domain.EmailID) embedding.Record {
	t.Helper()
	source, err := s.Emails().Get(context.Background(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := embedding.Prepare(source.Email)
	if err != nil {
		t.Fatal(err)
	}
	return embedding.Record{Key: embedding.Key{AccountID: a, EmailID: id, Provider: "fixture/model-v1", Dimension: 2, Version: embedding.PreprocessingVersion, InputHash: p.Hash}, SourceHash: source.ContentHash, Vector: []float32{1, 2}}
}

func TestEmbeddingCacheLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := account(t, s, "embedding-a")
	b := account(t, s, "embedding-b")
	e := sampleEmail(a, "one")
	e.BodyText = "Useful body\n-- \nOld signature"
	id := saveEmail(t, s, e)
	p := &fixtureEmbedder{}
	service := embeddingService(t, s, p)
	first, err := service.EmbedEmail(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 0
	again, err := service.EmbedEmail(ctx, a, id)
	if err != nil || again[0] == 0 || p.calls != 1 {
		t.Fatal("cache miss or alias", err)
	}
	e.BodyText = "Useful body\n-- \nNew signature"
	e.Recipients = nil
	saveEmail(t, s, e)
	if _, err := service.EmbedEmail(ctx, a, id); err != nil || p.calls != 1 {
		t.Fatal("unchanged prepared text regenerated", err)
	}
	if _, err := service.EmbedEmail(ctx, b, id); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("account leak", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	service = embeddingService(t, s, p)
	if _, err := service.EmbedEmail(ctx, a, id); err != nil || p.calls != 1 {
		t.Fatal("restart lost cache", err)
	}
	e.Subject = "Changed subject"
	saveEmail(t, s, e)
	if count(t, s, "email_embeddings") != 0 {
		t.Fatal("stale vector retained")
	}
	if _, err := service.EmbedEmail(ctx, a, id); err != nil || p.calls != 2 {
		t.Fatal("changed text not regenerated", err)
	}
	p2 := &fixtureEmbedder{name: "fixture/model-v2"}
	if _, err := embeddingService(t, s, p2).EmbedEmail(ctx, a, id); err != nil || p2.calls != 1 {
		t.Fatal("model revision reused vector", err)
	}
	p3 := &fixtureEmbedder{dimension: 3, output: [][]float32{{1, 2, 3}}}
	if _, err := embeddingService(t, s, p3).EmbedEmail(ctx, a, id); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "email_embeddings") != 3 {
		t.Fatal("dimension/model isolation failed")
	}
	if _, err := s.db.Exec("DELETE FROM emails WHERE account_id=? AND id=?", a, id); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "email_embeddings") != 0 {
		t.Fatal("purge retained vectors")
	}
}

func TestEmbeddingBatchAndConcurrentCache(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "batch")
	ctx := context.Background()
	id1 := saveEmail(t, s, sampleEmail(a, "one"))
	e := sampleEmail(a, "two")
	e.Subject = "Different"
	id2 := saveEmail(t, s, e)
	p := &fixtureEmbedder{}
	service := embeddingService(t, s, p)
	if _, err := service.EmbedEmail(ctx, a, id1); err != nil {
		t.Fatal(err)
	}
	result, err := service.EmbedEmails(ctx, a, []domain.EmailID{id2, id1})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.batchSizes, []int{1, 1}) || result[0][0] == result[1][0] {
		t.Fatal("batch cache/order", p.batchSizes, result)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.EmbedEmail(ctx, a, id2); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p.calls != 2 {
		t.Fatal("concurrent cache regenerated")
	}
	if _, err := service.EmbedEmails(ctx, a, []domain.EmailID{id1, id1}); !errors.Is(err, embedding.ErrInput) {
		t.Fatal(err)
	}
}

func TestEmbeddingProviderFailuresAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *fixtureEmbedder
		want error
	}{
		{"raw failure", &fixtureEmbedder{fail: errors.New("sensitive provider response")}, embedding.ErrProvider},
		{"wrong count", &fixtureEmbedder{output: [][]float32{}}, embedding.ErrVector},
		{"wrong dimension", &fixtureEmbedder{output: [][]float32{{1}}}, embedding.ErrVector},
		{"nan", &fixtureEmbedder{output: [][]float32{{float32(math.NaN()), 1}}}, embedding.ErrVector},
		{"infinity", &fixtureEmbedder{output: [][]float32{{1, float32(math.Inf(1))}}}, embedding.ErrVector},
		{"zero", &fixtureEmbedder{output: [][]float32{{0, 0}}}, embedding.ErrVector},
		{"cancel", &fixtureEmbedder{fail: context.Canceled}, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			a := account(t, s, "failure")
			id := saveEmail(t, s, sampleEmail(a, "one"))
			_, err := embeddingService(t, s, tc.p).EmbedEmails(context.Background(), a, []domain.EmailID{id})
			if err != tc.want || count(t, s, "email_embeddings") != 0 {
				t.Fatal(err)
			}
		})
	}
	s := testStore(t)
	a := account(t, s, "cancel")
	id := saveEmail(t, s, sampleEmail(a, "one"))
	p := &fixtureEmbedder{}
	service := embeddingService(t, s, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.EmbedEmail(ctx, a, id); !errors.Is(err, context.Canceled) || p.calls != 0 {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	p.hook = cancel
	if _, err := service.EmbedEmail(ctx, a, id); !errors.Is(err, context.Canceled) || count(t, s, "email_embeddings") != 0 {
		t.Fatal(err)
	}
	if _, err := embedding.NewLocalService(nil, s.Emails(), s.Vectors()); err != embedding.ErrUnavailable {
		t.Fatal(err)
	}
}

func TestEmbeddingStaleWritesAtomicityAndIsolation(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "stale")
	b := account(t, s, "other")
	ctx := context.Background()
	e := sampleEmail(a, "one")
	id := saveEmail(t, s, e)
	id2 := saveEmail(t, s, sampleEmail(a, "two"))
	valid := vectorRecord(t, s, a, id2)
	stale := vectorRecord(t, s, a, id)
	e.BodyText = "Changed"
	saveEmail(t, s, e)
	if err := s.Vectors().PutBatch(ctx, []embedding.Record{valid, stale}); !errors.Is(err, repository.ErrConflict) || count(t, s, "email_embeddings") != 0 {
		t.Fatal("non-atomic stale write", err)
	}
	wrong := valid
	wrong.Key.AccountID = b
	if err := s.Vectors().PutBatch(ctx, []embedding.Record{wrong}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account save", err)
	}
	wrong = valid
	wrong.Key.InputHash = stale.Key.InputHash
	// Different input hashes must be rejected even if the source guard is correct.
	wrong.Key.InputHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := s.Vectors().PutBatch(ctx, []embedding.Record{wrong}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal(err)
	}
	p := &fixtureEmbedder{hook: func() { e.Subject = "Raced"; saveEmail(t, s, e) }}
	if _, err := embeddingService(t, s, p).EmbedEmail(ctx, a, id); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale provider result saved", err)
	}
	if err := s.Vectors().PutBatch(ctx, []embedding.Record{valid}); err != nil {
		t.Fatal(err)
	}
	key := valid.Key
	key.AccountID = b
	if _, err := s.Vectors().Get(ctx, key); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
}

func TestEmbeddingConcurrentMissAndInvalidBatch(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "concurrent")
	ctx := context.Background()
	id := saveEmail(t, s, sampleEmail(a, "one"))
	id2 := saveEmail(t, s, sampleEmail(a, "two"))
	p := &fixtureEmbedder{}
	service := embeddingService(t, s, p)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.EmbedEmail(ctx, a, id); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p.calls != 1 {
		t.Fatal("duplicate concurrent inference", p.calls)
	}
	// A batch with a valid first output and malformed second output saves neither.
	p = &fixtureEmbedder{name: "fixture/invalid-batch", output: [][]float32{{1, 2}, {1}}}
	if _, err := embeddingService(t, s, p).EmbedEmails(ctx, a, []domain.EmailID{id, id2}); err != embedding.ErrVector {
		t.Fatal(err)
	}
	if count(t, s, "email_embeddings") != 1 {
		t.Fatal("partially cached invalid batch")
	}
	e, err := s.Emails().Get(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	e.Email.Labels = nil
	saveEmail(t, s, e.Email)
	if _, err := service.EmbedEmail(ctx, a, id); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "email_embeddings") != 1 {
		t.Fatal("label change removed cache")
	}
	e.Email.BodyState = "unavailable"
	saveEmail(t, s, e.Email)
	if count(t, s, "email_embeddings") != 0 {
		t.Fatal("unavailable body retained vectors")
	}
	if _, err := service.EmbedEmail(ctx, a, id); err != embedding.ErrUnavailable {
		t.Fatal(err)
	}
}

func TestEmbeddingProviderIdentityAndStorageFailures(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "identity")
	id := saveEmail(t, s, sampleEmail(a, "one"))
	ctx := context.Background()
	p := &fixtureEmbedder{dimension: embedding.MaxDimension + 1}
	if _, err := embedding.NewLocalService(p, s.Emails(), s.Vectors()); err != embedding.ErrInput {
		t.Fatal(err)
	}
	p = &fixtureEmbedder{}
	service := embeddingService(t, s, p)
	p.name = "changed"
	if _, err := service.EmbedEmail(ctx, a, id); err != embedding.ErrProvider || p.calls != 0 {
		t.Fatal(err)
	}
	p = &fixtureEmbedder{}
	service = embeddingService(t, s, p)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EmbedEmail(ctx, a, id); err != repository.ErrStorage || p.calls != 0 {
		t.Fatal("storage error treated as miss", err)
	}
}
