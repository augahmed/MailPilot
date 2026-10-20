// Package retrieval provides shared local vector retrieval, independent of AI
// generation and classification. It owns no index, scheduler or database.
package retrieval

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/model"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

const MaxResults = 20
const MaxEvidence = 3

var ErrInput = errors.New("invalid search input")

type QueryEmbedder interface {
	Descriptor() model.Descriptor
	EmbedQuery(context.Context, string) ([]float32, error)
}
type Evidence struct {
	ChunkID        string  `json:"chunkId"`
	Score          float64 `json:"score"`
	Excerpt        string  `json:"excerpt"`
	SourceRevision string  `json:"sourceRevision"`
}
type EmailResult struct {
	ReaderRevision string     `json:"readerRevision"`
	EmailID        string     `json:"emailId"`
	Subject        string     `json:"subject"`
	Sender         string     `json:"sender"`
	ReceivedAt     int64      `json:"receivedAt"`
	Evidence       []Evidence `json:"evidence"`
}
type Result struct {
	Emails  []EmailResult
	Sources []repository.VectorSource
	Scanned int
	Elapsed time.Duration
}
type VectorSearch struct {
	Vectors  repository.VectorRepository
	Embedder QueryEmbedder
	Profile  repository.IndexProfile
}

func New(v repository.VectorRepository, e QueryEmbedder) *VectorSearch {
	d := e.Descriptor()
	return &VectorSearch{v, e, repository.IndexProfile{Model: repository.EmbeddingModel{ID: d.ModelID, Version: d.Version, Dimension: d.Dimensions}, PreprocessingVersion: mailtext.PreprocessingVersion, IndexVersion: mailtext.ChunkIndexVersion, Fingerprint: d.PreprocessingFingerprint}}
}
func ValidQuery(q string, k int, f repository.SearchFilter) bool {
	return utf8.ValidString(q) && strings.TrimSpace(q) != "" && len(q) <= 2048 && !strings.ContainsRune(q, 0) && k >= 1 && k <= MaxResults && utf8.ValidString(f.Sender) && len(f.Sender) <= 200 && !strings.ContainsRune(f.Sender, 0) && len(f.CategoryID) <= 100 && utf8.ValidString(f.CategoryID) && !strings.ContainsRune(f.CategoryID, 0) && f.After >= 0 && f.Before >= 0 && f.After <= 253402300799999 && f.Before <= 253402300799999 && (f.Before == 0 || f.After < f.Before)
}

type hit struct {
	source repository.VectorSource
	score  float64
}

func better(a, b hit) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.source.Chunk.EmailID != b.source.Chunk.EmailID {
		return a.source.Chunk.EmailID < b.source.Chunk.EmailID
	}
	if a.source.Chunk.Index != b.source.Chunk.Index {
		return a.source.Chunk.Index < b.source.Chunk.Index
	}
	return a.source.Chunk.ID < b.source.Chunk.ID
}
func norm(v []float32) float64 {
	var n float64
	for _, f := range v {
		x := float64(f)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0
		}
		n += x * x
	}
	return math.Sqrt(n)
}

// Cosine uses float64 accumulation and supports finite, non-unit stored vectors.
func Cosine(a, b []float32) (float64, error) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, embedding.ErrVector
	}
	na, nb := norm(a), norm(b)
	if na == 0 || nb == 0 {
		return 0, embedding.ErrVector
	}
	var dot float64
	for i, f := range a {
		dot += float64(f) * float64(b[i])
	}
	return math.Max(-1, math.Min(1, dot/(na*nb))), nil
}
func overlaps(a, b repository.EmailChunk) bool {
	if a.ContentHash == b.ContentHash && a.Text == b.Text {
		return true
	}
	if a.SourceKind == "" || a.SourceKind != b.SourceKind || a.SectionIndex != b.SectionIndex {
		return false
	}
	return a.StartOffset < b.EndOffset && b.StartOffset < a.EndOffset
}

// Search ranks distinct emails by their maximum chunk cosine (no thread-count
// boost). It streams messages in order and retains O(K * evidence) candidates,
// suppressing overlapping passages only within the same email. Distinct replies,
// including conflicting evidence in the same thread, remain independently visible.
func (s *VectorSearch) Search(ctx context.Context, account domain.AccountID, q string, k int, f repository.SearchFilter) (Result, error) {
	start := time.Now()
	result := Result{Emails: []EmailResult{}}
	query, err := s.queryVector(ctx, account, q, k, f)
	if err != nil {
		return result, err
	}
	type group struct{ hits []hit }
	groups := []group{}
	current := []hit{}
	var email domain.EmailID
	flush := func() {
		if len(current) == 0 {
			return
		}
		groups = append(groups, group{current})
		sort.Slice(groups, func(i, j int) bool { return better(groups[i].hits[0], groups[j].hits[0]) })
		if len(groups) > k {
			groups = groups[:k]
		}
		current = nil
	}
	err = s.Vectors.ScanVectors(ctx, account, s.Profile, f, func(v repository.VectorSource) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if v.Chunk.AccountID != account {
			return repository.ErrConflict
		}
		if email != v.Chunk.EmailID {
			flush()
			email = v.Chunk.EmailID
		}
		score, err := Cosine(query, v.Vector)
		if err != nil {
			return err
		}
		result.Scanned++
		v.Vector = nil // never retain corpus vectors
		candidate := hit{v, score}
		// Highest score wins before overlap suppression. Retain the best passage even
		// if a later overlapping chunk arrives with a higher score.
		current = append(current, candidate)
		sort.Slice(current, func(i, j int) bool { return better(current[i], current[j]) })
		kept := make([]hit, 0, MaxEvidence)
		for _, h := range current {
			duplicate := false
			for _, prior := range kept {
				if overlaps(h.source.Chunk, prior.source.Chunk) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				kept = append(kept, h)
			}
			if len(kept) == MaxEvidence {
				break
			}
		}
		current = kept
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	flush()
	for _, g := range groups {
		best := g.hits[0].source
		e := EmailResult{ReaderRevision: mailtext.ReaderRevision(best.Chunk.SourceRevision), EmailID: string(best.Chunk.EmailID), Subject: clip(best.Subject, 240), Sender: clip(best.SenderName+" <"+best.SenderAddress+">", 240), ReceivedAt: best.ReceivedAt, Evidence: []Evidence{}}
		for _, h := range g.hits {
			e.Evidence = append(e.Evidence, Evidence{h.source.Chunk.ID, h.score, clip(h.source.Chunk.Text, 600), h.source.Chunk.SourceRevision})
			source := h.source
			source.Chunk.Text = ""
			source.Chunk.ContextText = ""
			result.Sources = append(result.Sources, source)
		}
		result.Emails = append(result.Emails, e)
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	if err = s.Vectors.CheckVectorSources(ctx, account, s.Profile, f, result.Sources); err != nil {
		return Result{}, err
	}
	result.Elapsed = time.Since(start)
	return result, nil
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
