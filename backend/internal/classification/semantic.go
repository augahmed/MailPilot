package classification

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
)

// SemanticEmbeddings keeps model-specific inference behind the embedding layer.
type SemanticEmbeddings interface {
	ProviderName() string
	Dimension() int
	EmbedEmail(context.Context, domain.AccountID, domain.EmailID) ([]float32, error)
	EmbedCategories(context.Context, domain.AccountID, []domain.EmailCategory) ([][]float32, error)
}
type SemanticConfig struct {
	MinimumSimilarity float64
	AmbiguityMargin   float64
	TopCandidates     int
}

func DefaultSemanticConfig() SemanticConfig { return SemanticConfig{.45, .05, 3} }

type SemanticClassifier struct {
	embeddings SemanticEmbeddings
	config     SemanticConfig
	version    string
}

func NewSemanticClassifier(e SemanticEmbeddings, c SemanticConfig) (*SemanticClassifier, error) {
	if e == nil || e.Dimension() < 1 || e.Dimension() > embedding.MaxDimension || e.ProviderName() == "" || !finite(c.MinimumSimilarity) || c.MinimumSimilarity <= 0 || c.MinimumSimilarity > 1 || !finite(c.AmbiguityMargin) || c.AmbiguityMargin < 0 || c.AmbiguityMargin > 2 || c.TopCandidates < 1 || c.TopCandidates > 5 {
		return nil, ErrInvalidConfig
	}
	return &SemanticClassifier{e, c, configVersion(struct {
		Provider                             string
		Dimension                            int
		Preprocessing, CategoryPreprocessing string
		Policy                               SemanticConfig
	}{e.ProviderName(), e.Dimension(), embedding.PreprocessingVersion, embedding.CategoryPreprocessingVersion, c})}, nil
}
func (c *SemanticClassifier) Name() string    { return "SemanticClassifier" }
func (c *SemanticClassifier) Version() string { return c.version }
func (c *SemanticClassifier) Classify(ctx context.Context, e domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	run := newRun(c, e, categories)
	run.UsedClassifier = "none"
	if err := ctx.Err(); err != nil {
		return domain.ClassificationRun{}, err
	}
	if e.ID == "" || e.Validate() != nil || len(categories) > 1000 {
		return domain.ClassificationRun{}, ErrInvalidInput
	}
	enabled := []domain.EmailCategory{}
	seen := map[domain.CategoryID]bool{}
	for _, category := range categories {
		if seen[category.ID] || category.ID == "" {
			return domain.ClassificationRun{}, ErrInvalidInput
		}
		seen[category.ID] = true
		if category.Enabled {
			if _, err := embedding.PrepareCategory(category); err != nil {
				return domain.ClassificationRun{}, ErrInvalidInput
			}
			enabled = append(enabled, category)
		}
	}
	if len(enabled) == 0 {
		run.Reason = "No enabled categories are available."
		return run, nil
	}
	if e.BodyState != "complete" {
		run.Reason = "Semantic classification requires a complete cached body."
		return run, nil
	}
	vector, err := c.embeddings.EmbedEmail(ctx, e.AccountID, e.ID)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	descriptions, err := c.embeddings.EmbedCategories(ctx, e.AccountID, enabled)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	if len(descriptions) != len(enabled) || embedding.ValidateVector(vector, c.embeddings.Dimension()) != nil {
		return domain.ClassificationRun{}, embedding.ErrVector
	}
	candidates := make([]domain.ClassificationCandidate, 0, len(enabled))
	for i, category := range enabled {
		if err := ctx.Err(); err != nil {
			return domain.ClassificationRun{}, err
		}
		score, err := CosineSimilarity(vector, descriptions[i])
		if err != nil {
			return domain.ClassificationRun{}, err
		}
		candidates = append(candidates, domain.ClassificationCandidate{CategoryID: category.ID, Similarity: score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Similarity != candidates[j].Similarity {
			return candidates[i].Similarity > candidates[j].Similarity
		}
		return candidates[i].CategoryID < candidates[j].CategoryID
	})
	run.Candidates = append(run.Candidates, candidates[:min(len(candidates), c.config.TopCandidates)]...)
	best := candidates[0]
	if best.Similarity < c.config.MinimumSimilarity {
		run.Reason = fmt.Sprintf("Best cosine similarity %.3f is below the %.3f minimum.", best.Similarity, c.config.MinimumSimilarity)
		return run, nil
	}
	if len(candidates) > 1 && best.Similarity-candidates[1].Similarity <= c.config.AmbiguityMargin {
		run.Reason = fmt.Sprintf("Leading categories are within the %.3f ambiguity margin; no category chosen.", c.config.AmbiguityMargin)
		return run, nil
	}
	run.UsedClassifier = c.Name()
	run.Reason = fmt.Sprintf("Category description has cosine similarity %.3f, above %.3f, with a clear lead.", best.Similarity, c.config.MinimumSimilarity)
	run.Results = []domain.ClassificationResult{{AccountID: e.AccountID, EmailID: e.ID, CategoryID: best.CategoryID, Confidence: best.Similarity, ScoreKind: "cosine_similarity", ClassifierName: c.Name(), ClassifierVersion: c.Version(), InputHash: run.InputHash, Reasons: []string{run.Reason}, Timestamp: run.Timestamp}}
	return run, nil
}
func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// CosineSimilarity accepts unnormalized vectors and accumulates in float64.
func CosineSimilarity(a, b []float32) (float64, error) {
	if embedding.ValidateVector(a, len(a)) != nil || embedding.ValidateVector(b, len(a)) != nil {
		return 0, embedding.ErrVector
	}
	var dot, aa, bb float64
	for i, x := range a {
		y := float64(b[i])
		xx := float64(x)
		dot += xx * y
		aa += xx * xx
		bb += y * y
	}
	score := dot / (math.Sqrt(aa) * math.Sqrt(bb))
	if !finite(score) {
		return 0, embedding.ErrVector
	}
	return max(-1, min(1, score)), nil
}
func newRun(c ClassificationEngine, e domain.Email, categories []domain.EmailCategory) domain.ClassificationRun {
	return domain.ClassificationRun{AccountID: e.AccountID, EmailID: e.ID, ClassifierName: c.Name(), ClassifierVersion: c.Version(), InputHash: InputHash(e, categories), Timestamp: time.Now().UTC().Truncate(time.Millisecond), Results: []domain.ClassificationResult{}}
}
