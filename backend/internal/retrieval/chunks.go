package retrieval

import (
	"container/heap"
	"context"
	"sort"
	"strings"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

// ChunkMatch is an exact chunk-level nearest neighbor, before email aggregation.
// Source retains original metadata, full passage and revision; Vector is omitted.
type ChunkMatch struct {
	Source repository.VectorSource
	Score  float64
}
type hitHeap []hit

func (h hitHeap) Len() int           { return len(h) }
func (h hitHeap) Less(i, j int) bool { return better(h[j], h[i]) } // worst at root
func (h hitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *hitHeap) Push(v any)        { *h = append(*h, v.(hit)) }
func (h *hitHeap) Pop() any          { last := len(*h) - 1; v := (*h)[last]; *h = (*h)[:last]; return v }

// SearchChunks returns the exact K highest cosine chunk vectors with stable
// email/ordinal/ID ties, in O(K) retained memory. It deliberately preserves
// overlaps; Search provides distinct email results and overlap suppression for UI
// and evidence consumers. Both use the same repository and local query encoder.
func (s *VectorSearch) SearchChunks(ctx context.Context, account domain.AccountID, q string, k int, f repository.SearchFilter) ([]ChunkMatch, error) {
	query, err := s.queryVector(ctx, account, q, k, f)
	if err != nil {
		return nil, err
	}
	top := &hitHeap{}
	heap.Init(top)
	err = s.Vectors.ScanVectors(ctx, account, s.Profile, f, func(v repository.VectorSource) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if v.Chunk.AccountID != account {
			return repository.ErrConflict
		}
		score, err := Cosine(query, v.Vector)
		if err != nil {
			return err
		}
		v.Vector = nil
		h := hit{source: v, score: score}
		if top.Len() < k {
			heap.Push(top, h)
		} else if better(h, (*top)[0]) {
			(*top)[0] = h
			heap.Fix(top, 0)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(*top, func(i, j int) bool { return better((*top)[i], (*top)[j]) })
	matches := make([]ChunkMatch, 0, top.Len())
	sources := make([]repository.VectorSource, 0, top.Len())
	for _, h := range *top {
		matches = append(matches, ChunkMatch{h.source, h.score})
		sources = append(sources, h.source)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = s.Vectors.CheckVectorSources(ctx, account, s.Profile, f, sources); err != nil {
		return nil, err
	}
	return matches, nil
}
func (s *VectorSearch) queryVector(ctx context.Context, account domain.AccountID, q string, k int, f repository.SearchFilter) ([]float32, error) {
	if account == "" || !ValidQuery(q, k, f) {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query, err := s.Embedder.EmbedQuery(ctx, strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	if len(query) != s.Profile.Model.Dimension || norm(query) == 0 {
		return nil, embedding.ErrVector
	}
	return query, nil
}
