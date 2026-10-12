package embedding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

const CategoryPreprocessingVersion = "category-description-v1"

type CategoryKey struct {
	AccountID  domain.AccountID
	CategoryID domain.CategoryID
	Provider   string
	Dimension  int
	Version    string
	InputHash  string
}
type CategoryRecord struct {
	Key    CategoryKey
	Vector []float32
}
type CategoryVectorStore interface {
	GetCategory(context.Context, CategoryKey) ([]float32, error)
	PutCategories(context.Context, []CategoryRecord) error
}

func (k CategoryKey) Validate() error {
	if k.AccountID == "" || k.CategoryID == "" || !validProvider(k.Provider, k.Dimension) || k.Version != CategoryPreprocessingVersion || !validHash(k.InputHash) {
		return ErrInput
	}
	return nil
}
func PrepareCategory(c domain.EmailCategory) (PreparedText, error) {
	if c.ID == "" || !c.Enabled || len(c.Description) > 1000 || !utf8.ValidString(c.Description) {
		return PreparedText{}, ErrInput
	}
	text := clean(c.Description)
	if strings.TrimSpace(text) == "" {
		return PreparedText{}, ErrInput
	}
	return PreparedText{Text: text, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(CategoryPreprocessingVersion+"\x00"+text)))}, nil
}

// NewLocalClassificationService adds persistent category-description caching to
// the same provider and vector space used for email embeddings.
func NewLocalClassificationService(p EmbeddingProvider, emails EmailReader, vectors VectorStore, categories CategoryVectorStore) (*Service, error) {
	s, err := NewLocalService(p, emails, vectors)
	if err != nil {
		return nil, err
	}
	if categories == nil {
		return nil, ErrInput
	}
	s.categories = categories
	return s, nil
}
func (s *Service) ProviderName() string { return s.name }
func (s *Service) Dimension() int       { return s.dimension }
func (s *Service) EmbedCategories(ctx context.Context, account domain.AccountID, categories []domain.EmailCategory) ([][]float32, error) {
	if s.categories == nil {
		return nil, ErrUnavailable
	}
	if account == "" || len(categories) == 0 || len(categories) > 1000 {
		return nil, ErrInput
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
	result := make([][]float32, len(categories))
	seen := map[domain.CategoryID]bool{}
	var texts []string
	var records []CategoryRecord
	var positions []int
	for i, c := range categories {
		if seen[c.ID] {
			return nil, ErrInput
		}
		seen[c.ID] = true
		prepared, err := PrepareCategory(c)
		if err != nil {
			return nil, err
		}
		key := CategoryKey{account, c.ID, s.name, s.dimension, CategoryPreprocessingVersion, prepared.Hash}
		v, err := s.categories.GetCategory(ctx, key)
		if err == nil {
			if err = ValidateVector(v, s.dimension); err != nil {
				return nil, err
			}
			result[i] = append([]float32(nil), v...)
			continue
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		texts = append(texts, prepared.Text)
		records = append(records, CategoryRecord{Key: key})
		positions = append(positions, i)
	}
	// Provider batches remain bounded; do not write any categories until every
	// output is validated. PutCategories commits the selected set atomically.
	for start := 0; start < len(texts); start += MaxBatchSize {
		end := min(start+MaxBatchSize, len(texts))
		vectors, err := s.provider.EmbedBatch(ctx, texts[start:end])
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, safeProviderError(err)
		}
		if len(vectors) != end-start {
			return nil, ErrVector
		}
		for j, v := range vectors {
			if err := ValidateVector(v, s.dimension); err != nil {
				return nil, err
			}
			i := start + j
			records[i].Vector = append([]float32(nil), v...)
			result[positions[i]] = append([]float32(nil), v...)
		}
	}
	if len(records) > 0 {
		if err := s.categories.PutCategories(ctx, records); err != nil {
			return nil, err
		}
	}
	return result, ctx.Err()
}
