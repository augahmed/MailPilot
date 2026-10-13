package classification

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

	"mailpilot.local/core/internal/domain"
)

// These are evidence-strength gates, not calibrated probabilities. A specific
// three-condition starter rule scores .60; a broad two-condition rule scores
// .55 and needs semantic corroboration. Cosines are never added to rule scores.
type HybridConfig struct {
	RuleThreshold              float64
	ReinforcementRuleThreshold float64
	ReinforcementSimilarity    float64
}

func DefaultHybridConfig() HybridConfig {
	return HybridConfig{RuleThreshold: .60, ReinforcementRuleThreshold: .55, ReinforcementSimilarity: .35}
}

type HybridClassifier struct {
	rules    ClassificationEngine
	semantic *SemanticClassifier
	config   HybridConfig
	version  string
}

func NewHybridClassifier(rules ClassificationEngine, semantic *SemanticClassifier, c HybridConfig) (*HybridClassifier, error) {
	if rules == nil || !finite(c.RuleThreshold) || c.RuleThreshold <= 0 || c.RuleThreshold > 1 || !finite(c.ReinforcementRuleThreshold) || c.ReinforcementRuleThreshold <= 0 || c.ReinforcementRuleThreshold > c.RuleThreshold || !finite(c.ReinforcementSimilarity) || c.ReinforcementSimilarity <= 0 || c.ReinforcementSimilarity > 1 {
		return nil, ErrInvalidConfig
	}
	if semantic != nil && c.ReinforcementSimilarity > semantic.config.MinimumSimilarity {
		return nil, ErrInvalidConfig
	}
	semanticVersion := "unconfigured"
	if semantic != nil {
		semanticVersion = semantic.Version()
	}
	return &HybridClassifier{rules, semantic, c, configVersion(struct {
		Algorithm, RulesName, RulesVersion, SemanticVersion string
		Policy                                              HybridConfig
	}{"hybrid-2", rules.Name(), rules.Version(), semanticVersion, c})}, nil
}
func configVersion(config any) string {
	data, _ := json.Marshal(config)
	return fmt.Sprintf("v1-%x", sha256.Sum256(data))
}
func (c *HybridClassifier) Name() string    { return "HybridClassifier" }
func (c *HybridClassifier) Version() string { return c.version }
func (c *HybridClassifier) Classify(ctx context.Context, e domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	run, err := c.classify(ctx, e, categories)
	if c.semantic != nil && len(run.Candidates) > c.semantic.config.TopCandidates {
		run.Candidates = run.Candidates[:c.semantic.config.TopCandidates]
	}
	return run, err
}
func (c *HybridClassifier) classify(ctx context.Context, e domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	var evidence ruleEvidence
	var err error
	if rules, ok := c.rules.(*RuleBasedClassifier); ok {
		evidence, err = rules.evaluate(ctx, e, categories)
	} else {
		evidence.run, err = c.rules.Classify(ctx, e, categories)
	}
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	run := newRun(c, e, categories)
	run.UsedClassifier = "none"
	if evidence.ambiguous {
		run.Reason = "Specific rules match competing categories; semantic similarity cannot erase that conflict."
		return run, nil
	}
	var matched *domain.ClassificationResult
	if len(evidence.run.Results) == 1 {
		r := evidence.run.Results[0]
		matched = &r
	}
	accept := func(result domain.ClassificationResult, used, reason string) domain.ClassificationRun {
		run.UsedClassifier = used
		run.Reason = reason
		result.ClassifierName = c.Name()
		result.ClassifierVersion = c.Version()
		result.InputHash = run.InputHash
		result.Timestamp = run.Timestamp
		// Keep the measured score's original kind, and record the other evidence in
		// explanations. This avoids presenting a manufactured blended probability.
		if !slices.Contains(result.Reasons, reason) {
			result.Reasons = append(append([]string{}, result.Reasons[:min(5, len(result.Reasons))]...), reason)
		}
		run.Results = []domain.ClassificationResult{result}
		return run
	}
	strong := matched != nil && matched.Confidence >= c.config.RuleThreshold
	if c.semantic == nil {
		if strong {
			return accept(*matched, c.rules.Name(), fmt.Sprintf("Specific rule strength %.2f meets the %.2f direct-rule gate; local embeddings are not configured.", matched.Confidence, c.config.RuleThreshold)), nil
		}
		run.Reason = "No specific rule met the direct-rule gate and local embeddings are not configured."
		return run, nil
	}
	semantic, err := c.semantic.classify(ctx, e, categories)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	run.Candidates = semantic.Candidates
	if len(semantic.Candidates) == 0 {
		run.Reason = semantic.Reason
		return run, nil
	}
	best := semantic.Candidates[0]
	// An explicit rule exclusion is negative evidence even when no rule won.
	// Do not silently choose the runner-up after vetoing the leading category.
	if evidence.excluded[best.CategoryID] {
		run.Reason = "The leading semantic category has an explicit rule exclusion; no category chosen."
		return run, nil
	}
	// Classify retains at least two candidates internally for this comparison,
	// regardless of how many are selected for presentation.
	clear := len(semantic.Candidates) < 2 || best.Similarity-semantic.Candidates[1].Similarity > c.semantic.config.AmbiguityMargin
	supported := matched != nil && matched.Confidence >= c.config.ReinforcementRuleThreshold
	if supported && best.Similarity >= c.config.ReinforcementSimilarity {
		if matched.CategoryID != best.CategoryID {
			run.Reason = "Rule evidence and the leading semantic category disagree; no category chosen."
			return run, nil
		}
		if !clear {
			run.Reason = "Semantic candidates are too close to corroborate the rule; no category chosen."
			return run, nil
		}
		result := *matched
		result.ScoreKind = "cosine_similarity"
		result.Confidence = best.Similarity
		reason := fmt.Sprintf("Rule strength %.2f and cosine %.3f agree; cosine meets %.3f with a lead above %.3f.", matched.Confidence, best.Similarity, c.config.ReinforcementSimilarity, c.semantic.config.AmbiguityMargin)
		result.Reasons = append(append([]string{}, matched.Reasons[:min(5, len(matched.Reasons))]...), reason)
		return accept(result, c.Name(), reason), nil
	}
	if strong {
		// Low semantic evidence is inconclusive, not a competing category claim.
		return accept(*matched, c.rules.Name(), fmt.Sprintf("Specific rule strength %.2f meets %.2f; semantic evidence is below the %.3f corroboration gate.", matched.Confidence, c.config.RuleThreshold, c.config.ReinforcementSimilarity)), nil
	}
	run.Reason = semantic.Reason
	if len(semantic.Results) == 1 {
		return accept(semantic.Results[0], c.semantic.Name(), semantic.Reason), nil
	}
	return run, nil
}
