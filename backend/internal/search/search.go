// Package search performs local-only retrieval over persisted email embeddings.
package search

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

const MaxTopK = 20
const MaxScan = 10000
const PageSize = 32

var ErrInput = errors.New("invalid semantic search input")

type Model struct {
	Provider          string
	Dimension         int
	Version           string
	ClassifierName    string
	ClassifierVersion string
}

func (m Model) Validate() error {
	return (embedding.Key{AccountID: "check", EmailID: "check", Provider: m.Provider, Dimension: m.Dimension, Version: m.Version, InputHash: strings.Repeat("0", 64)}).Validate()
}

type Filters struct {
	From, Until *int64
	Sender      string
	CategoryID  domain.CategoryID
}

func (f Filters) Validate() error {
	if len(f.Sender) > 256 || !utf8.ValidString(f.Sender) || len(f.CategoryID) > 100 {
		return ErrInput
	}
	for _, value := range []*int64{f.From, f.Until} {
		if value != nil && (*value < 0 || *value > 253402300800000) {
			return ErrInput
		}
	}
	if f.From != nil && f.Until != nil && *f.From >= *f.Until {
		return ErrInput
	}
	return nil
}

type Counts struct {
	Cached   int `json:"cached"`
	Eligible int `json:"eligible"`
	Indexed  int `json:"indexed"`
}
type Candidate struct {
	Stored    repository.StoredEmail
	Vector    []float32
	InputHash string
	Valid     bool
}
type Page struct {
	Candidates []Candidate
	After      domain.EmailID
	Done       bool
	Scanned    int
}

// Repository restricts the persistent index and every source read to one account.
// Current rechecks content and filter membership before returning an excerpt.
type Repository interface {
	Counts(context.Context, domain.AccountID, Model) (Counts, error)
	Missing(context.Context, domain.AccountID, Model, domain.EmailID, int) ([]domain.EmailID, error)
	Candidates(context.Context, domain.AccountID, Model, Filters, domain.EmailID, int) (Page, error)
	Current(context.Context, domain.AccountID, Model, Filters, domain.EmailID) (repository.StoredEmail, error)
}
type Embeddings interface {
	ProviderName() string
	Dimension() int
	EmbedEmails(context.Context, domain.AccountID, []domain.EmailID) ([][]float32, error)
	EmbedTexts(context.Context, []string) ([][]float32, error)
}
type Service struct {
	Index      Repository
	Embeddings Embeddings
	Model      Model
}

func New(index Repository, embeddings Embeddings, classifier ClassificationIdentity) *Service {
	m := Model{Provider: "unconfigured", Dimension: 1, Version: embedding.PreprocessingVersion}
	if embeddings != nil {
		m.Provider = embeddings.ProviderName()
		m.Dimension = embeddings.Dimension()
	}
	if classifier != nil {
		m.ClassifierName = classifier.Name()
		m.ClassifierVersion = classifier.Version()
	}
	return &Service{index, embeddings, m}
}

type ClassificationIdentity interface {
	Name() string
	Version() string
}
type Hit struct {
	Stored     repository.StoredEmail
	Similarity float64
	Excerpt    string
}
type Result struct {
	Hits            []Hit
	Scanned, Errors int
	Partial         bool
	ExcerptFallback bool
}

func (s *Service) Query(ctx context.Context, account domain.AccountID, query string, k int, filters Filters) (Result, error) {
	result := Result{Hits: []Hit{}}
	if account == "" || len(query) > 1000 || !utf8.ValidString(query) || strings.TrimSpace(query) == "" || k < 1 || k > MaxTopK || filters.Validate() != nil {
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
		page, err := s.Index.Candidates(ctx, account, s.Model, filters, after, min(PageSize, MaxScan-result.Scanned))
		if err != nil {
			return result, err
		}
		if !page.Done && page.After <= after {
			return result, repository.ErrStorage
		}
		result.Scanned += page.Scanned
		for _, candidate := range page.Candidates {
			if candidate.Stored.Email.AccountID != account {
				return result, repository.ErrStorage
			}
			if !candidate.Valid {
				result.Errors++
				continue
			}
			prepared, err := embedding.Prepare(candidate.Stored.Email)
			if err != nil || prepared.Hash != candidate.InputHash {
				result.Errors++
				continue
			}
			score, err := classification.CosineSimilarity(vectors[0], candidate.Vector)
			if err != nil {
				result.Errors++
				continue
			}
			result.Hits = append(result.Hits, Hit{Stored: candidate.Stored, Similarity: score})
			sort.Slice(result.Hits, func(i, j int) bool { return better(result.Hits[i], result.Hits[j]) })
			if len(result.Hits) > k {
				result.Hits = result.Hits[:k]
			}
		}
		// Scanned counts rows considered, including stale/filter-rejected rows.
		// Scanned may be larger than Candidates when a category is stale.
		after = page.After
		if page.Done {
			break
		}
		if result.Scanned >= MaxScan {
			result.Partial = true
			break
		}
	}
	fresh := []Hit{}
	for _, hit := range result.Hits {
		source, err := s.Index.Current(ctx, account, s.Model, filters, hit.Stored.Email.ID)
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
	// Passage vectors exist only in memory for this query; no chunk index or
	// model-generated excerpts are introduced.
	var passages []string
	var owners []int
	for i, hit := range result.Hits {
		prepared, err := embedding.Prepare(hit.Stored.Email)
		if err != nil {
			result.ExcerptFallback = true
			continue
		}
		snippets := excerpts(prepared.Text, query)
		if len(snippets) == 0 {
			result.Hits[i].Excerpt = clipDisplay(hit.Stored.Email.Subject, 160)
			continue
		}
		result.Hits[i].Excerpt = snippets[0]
		for _, snippet := range snippets {
			passages = append(passages, snippet)
			owners = append(owners, i)
		}
	}
	best := make([]float64, len(result.Hits))
	for i := range best {
		best[i] = -2
	}
	for start := 0; start < len(passages); start += embedding.MaxBatchSize {
		end := min(start+embedding.MaxBatchSize, len(passages))
		v, err := s.Embeddings.EmbedTexts(ctx, passages[start:end])
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err != nil || len(v) != end-start {
			result.ExcerptFallback = true
			break
		}
		for j, vector := range v {
			score, err := classification.CosineSimilarity(vectors[0], vector)
			if err != nil {
				result.ExcerptFallback = true
				continue
			}
			owner := owners[start+j]
			if score > best[owner] {
				best[owner] = score
				result.Hits[owner].Excerpt = passages[start+j]
			}
		}
	}
	return result, nil
}
func better(a, b Hit) bool {
	if a.Similarity != b.Similarity {
		return a.Similarity > b.Similarity
	}
	if !a.Stored.Email.ReceivedAt.Equal(b.Stored.Email.ReceivedAt) {
		return a.Stored.Email.ReceivedAt.After(b.Stored.Email.ReceivedAt)
	}
	return a.Stored.Email.ID < b.Stored.Email.ID
}
func clipDisplay(value string, n int) string {
	value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	r := []rune(value)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return value
}
