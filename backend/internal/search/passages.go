package search

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"sort"
	"strings"
	"unicode/utf8"
)

func (s *Service) queryPassages(ctx context.Context, a domain.AccountID, query string, k int, f Filters) (Result, error) {
	result := Result{Hits: []Hit{}}
	if a == "" || len(query) > 1000 || !utf8.ValidString(query) || strings.TrimSpace(query) == "" || k < 1 || k > MaxTopK || f.Validate() != nil {
		return result, ErrInput
	}
	if s.Embeddings == nil {
		return result, embedding.ErrUnavailable
	}
	vectors, err := s.Embeddings.EmbedTexts(ctx, []string{query})
	if err != nil {
		return result, err
	}
	if len(vectors) != 1 || embedding.ValidateVector(vectors[0], s.Model.Dimension) != nil {
		return result, embedding.ErrVector
	}
	after := domain.EmailID("")
	for result.Scanned < MaxScan {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		page, err := s.Index.Candidates(ctx, a, s.Model, f, after, min(PageSize, MaxScan-result.Scanned))
		if err != nil {
			return result, err
		}
		if !page.Done && page.After <= after {
			return result, repository.ErrStorage
		}
		result.Scanned += page.Scanned
		for _, candidate := range page.Candidates {
			if candidate.Stored.Email.AccountID != a {
				return result, repository.ErrStorage
			}
			if !candidate.Valid {
				result.Errors++
				continue
			}
			best := Hit{Stored: candidate.Stored, Similarity: -2}
			for _, chunk := range candidate.Chunks {
				if err := ctx.Err(); err != nil {
					return result, err
				}
				score, err := classification.CosineSimilarity(vectors[0], chunk.Vector)
				if err != nil {
					result.Errors++
					continue
				}
				if score > best.Similarity {
					best.Similarity = score
					best.Excerpt = chunk.Passage.Text
					best.PassageKind = chunk.Passage.Kind
					best.PassageIndex = chunk.Passage.Ordinal
					best.DedupKey = ""
					if candidate.Stored.Email.ProviderThreadID != "" {
						best.DedupKey = string(candidate.Stored.Email.ProviderThreadID) + "\x00" + chunk.Passage.Hash
					}
				}
			}
			if best.Similarity < -1 {
				continue
			}
			replaced := false
			if best.DedupKey != "" {
				for i, hit := range result.Hits {
					if hit.DedupKey == best.DedupKey {
						if better(best, hit) {
							result.Hits[i] = best
						}
						replaced = true
						break
					}
				}
			}
			if !replaced {
				result.Hits = append(result.Hits, best)
			}
			sort.Slice(result.Hits, func(i, j int) bool { return better(result.Hits[i], result.Hits[j]) })
			if len(result.Hits) > k {
				result.Hits = result.Hits[:k]
			}
		}
		after = page.After
		if page.Done {
			break
		}
		if result.Scanned >= MaxScan {
			result.Partial = true
		}
	}
	fresh := []Hit{}
	for _, hit := range result.Hits {
		source, err := s.Index.Current(ctx, a, s.Model, f, hit.Stored.Email.ID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrConflict) {
				result.Errors++
				continue
			}
			return result, err
		}
		if source.ContentHash != hit.Stored.ContentHash {
			result.Errors++
			continue
		}
		hit.Stored = source
		fresh = append(fresh, hit)
	}
	result.Hits = fresh
	result.Partial = result.Partial || result.Errors > 0
	return result, nil
}
