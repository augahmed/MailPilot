package search

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

type PassageVectors interface {
	Cached(context.Context, domain.AccountID, domain.EmailID, Model) (map[string][]float32, error)
	Replace(context.Context, repository.StoredEmail, Model, embedding.RetrievalText, map[string][]float32) error
}

// PassageEmbeddings reuses the trusted local inference service, while keeping
// retrieval's versioned cache and payloads independent of classification.
type PassageEmbeddings struct {
	Base    Embeddings
	Emails  embedding.EmailReader
	Vectors PassageVectors
}

func (p *PassageEmbeddings) ProviderName() string         { return p.Base.ProviderName() }
func (p *PassageEmbeddings) Dimension() int               { return p.Base.Dimension() }
func (p *PassageEmbeddings) PreprocessingVersion() string { return embedding.RetrievalVersion }
func (p *PassageEmbeddings) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return p.Base.EmbedTexts(ctx, texts)
}
func (p *PassageEmbeddings) EmbedEmails(ctx context.Context, a domain.AccountID, ids []domain.EmailID) ([][]float32, error) {
	if a == "" || len(ids) == 0 || len(ids) > embedding.MaxBatchSize {
		return nil, embedding.ErrInput
	}
	model := Model{Provider: p.ProviderName(), Dimension: p.Dimension(), Version: embedding.RetrievalVersion}
	result := make([][]float32, len(ids))
	seen := map[domain.EmailID]bool{}
	for i, id := range ids {
		if id == "" || seen[id] {
			return nil, embedding.ErrInput
		}
		seen[id] = true
		source, err := p.Emails.Get(ctx, a, id)
		if err != nil {
			return nil, err
		}
		if source.Email.AccountID != a || source.Email.ID != id {
			return nil, repository.ErrConflict
		}
		prepared, err := embedding.PrepareRetrieval(source.Email)
		if err != nil {
			return nil, err
		}
		cached, err := p.Vectors.Cached(ctx, a, id, model)
		if err != nil {
			return nil, err
		}
		values := map[string][]float32{}
		missing := []embedding.Passage{}
		for _, chunk := range prepared.Passages {
			if _, ok := values[chunk.Hash]; ok {
				continue
			}
			if v, ok := cached[chunk.Hash]; ok && embedding.ValidateVector(v, model.Dimension) == nil {
				values[chunk.Hash] = v
			} else {
				values[chunk.Hash] = nil
				missing = append(missing, chunk)
			}
		}
		for start := 0; start < len(missing); start += embedding.MaxBatchSize {
			end := min(start+embedding.MaxBatchSize, len(missing))
			texts := []string{}
			for _, chunk := range missing[start:end] {
				texts = append(texts, chunk.Text)
			}
			vectors, err := p.Base.EmbedTexts(ctx, texts)
			if err != nil {
				return nil, err
			}
			if len(vectors) != len(texts) {
				return nil, embedding.ErrVector
			}
			for j, v := range vectors {
				if embedding.ValidateVector(v, model.Dimension) != nil {
					return nil, embedding.ErrVector
				}
				values[missing[start+j].Hash] = v
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := p.Vectors.Replace(ctx, source, model, prepared, values); err != nil {
			return nil, err
		}
		if len(prepared.Passages) > 0 {
			result[i] = values[prepared.Passages[0].Hash]
		}
	}
	return result, nil
}
