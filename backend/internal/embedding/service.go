package embedding

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type EmailReader interface {
	Get(context.Context, domain.AccountID, domain.EmailID) (repository.StoredEmail, error)
}

type Service struct {
	provider   EmbeddingProvider
	emails     EmailReader
	vectors    VectorStore
	categories CategoryVectorStore
	name       string
	dimension  int
	gate       chan struct{}
}

// NewLocalService accepts an explicitly supplied, trusted on-device provider.
// No provider is configured by default. Callers must authorize account access;
// this internal service is not exposed over IPC or invoked by sync.
func NewLocalService(p EmbeddingProvider, emails EmailReader, vectors VectorStore) (*Service, error) {
	if p == nil {
		return nil, ErrUnavailable
	}
	if emails == nil || vectors == nil {
		return nil, ErrInput
	}
	name, dimension := p.ProviderName(), p.Dimension()
	if !validProvider(name, dimension) {
		return nil, ErrInput
	}
	return &Service{provider: p, emails: emails, vectors: vectors, name: name, dimension: dimension, gate: make(chan struct{}, 1)}, nil
}

func (s *Service) EmbedEmail(ctx context.Context, account domain.AccountID, id domain.EmailID) ([]float32, error) {
	v, err := s.embed(ctx, account, []domain.EmailID{id}, true)
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (s *Service) EmbedEmails(ctx context.Context, account domain.AccountID, ids []domain.EmailID) ([][]float32, error) {
	return s.embed(ctx, account, ids, false)
}

func (s *Service) embed(ctx context.Context, account domain.AccountID, ids []domain.EmailID, single bool) ([][]float32, error) {
	if account == "" || len(ids) == 0 || len(ids) > MaxBatchSize {
		return nil, ErrInput
	}
	seen := make(map[domain.EmailID]bool)
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, ErrInput
		}
		seen[id] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// One bounded call per service; waiting remains cancellable and concurrent
	// identical requests observe the cache after the first successful write.
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.provider.ProviderName() != s.name || s.provider.Dimension() != s.dimension {
		return nil, ErrProvider
	}
	result := make([][]float32, len(ids))
	var texts []string
	var records []Record
	var positions []int
	for i, id := range ids {
		source, err := s.emails.Get(ctx, account, id)
		if err != nil {
			return nil, err
		}
		if source.Email.AccountID != account || source.Email.ID != id {
			return nil, ErrInput
		}
		prepared, err := Prepare(source.Email)
		if err != nil {
			return nil, err
		}
		key := Key{account, id, s.name, s.dimension, PreprocessingVersion, prepared.Hash}
		cached, err := s.vectors.Get(ctx, key)
		if err == nil {
			if err = ValidateVector(cached, s.dimension); err != nil {
				return nil, err
			}
			result[i] = append([]float32(nil), cached...)
			continue
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		texts = append(texts, prepared.Text)
		records = append(records, Record{Key: key, SourceHash: source.ContentHash})
		positions = append(positions, i)
	}
	if len(texts) == 0 {
		return result, ctx.Err()
	}
	var vectors [][]float32
	var err error
	if single {
		var v []float32
		v, err = s.provider.EmbedText(ctx, texts[0])
		vectors = [][]float32{v}
	} else {
		vectors, err = s.provider.EmbedBatch(ctx, texts)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, safeProviderError(err)
	}
	if len(vectors) != len(records) {
		return nil, ErrVector
	}
	for i, v := range vectors {
		if err := ValidateVector(v, s.dimension); err != nil {
			return nil, err
		}
		records[i].Vector = append([]float32(nil), v...)
		result[positions[i]] = append([]float32(nil), v...)
	}
	if err := s.vectors.PutBatch(ctx, records); err != nil {
		return nil, err
	}
	return result, nil
}

func safeProviderError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrProvider
}

// EmbedTexts is a bounded, local-only query/passage operation. Text and vectors
// remain in memory; it shares the configured provider and cancellable gate.
func (s *Service) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > MaxBatchSize {
		return nil, ErrInput
	}
	for _, text := range texts {
		if len(text) > MaxTextBytes || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
			return nil, ErrInput
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.provider.ProviderName() != s.name || s.provider.Dimension() != s.dimension {
		return nil, ErrProvider
	}
	vectors, err := s.provider.EmbedBatch(ctx, texts)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, safeProviderError(err)
	}
	if len(vectors) != len(texts) {
		return nil, ErrVector
	}
	result := make([][]float32, len(vectors))
	for i, v := range vectors {
		if err := ValidateVector(v, s.dimension); err != nil {
			return nil, err
		}
		result[i] = append([]float32(nil), v...)
	}
	return result, nil
}
