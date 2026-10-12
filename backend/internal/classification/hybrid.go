package classification

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"mailpilot.local/core/internal/domain"
)

type HybridConfig struct{ RuleThreshold float64 }

func DefaultHybridConfig() HybridConfig { return HybridConfig{RuleThreshold: .85} }

type HybridClassifier struct {
	rules    ClassificationEngine
	semantic *SemanticClassifier
	config   HybridConfig
	version  string
}

func NewHybridClassifier(rules ClassificationEngine, semantic *SemanticClassifier, c HybridConfig) (*HybridClassifier, error) {
	if rules == nil || !finite(c.RuleThreshold) || c.RuleThreshold <= 0 || c.RuleThreshold > 1 {
		return nil, ErrInvalidConfig
	}
	semanticVersion := "unconfigured"
	if semantic != nil {
		semanticVersion = semantic.Version()
	}
	return &HybridClassifier{rules, semantic, c, configVersion(struct {
		RulesName, RulesVersion, SemanticVersion string
		Policy                                   HybridConfig
	}{rules.Name(), rules.Version(), semanticVersion, c})}, nil
}
func configVersion(config any) string {
	data, _ := json.Marshal(config)
	return fmt.Sprintf("v1-%x", sha256.Sum256(data))
}
func (c *HybridClassifier) Name() string    { return "HybridClassifier" }
func (c *HybridClassifier) Version() string { return c.version }
func (c *HybridClassifier) Classify(ctx context.Context, e domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	rules, err := c.rules.Classify(ctx, e, categories)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	run := newRun(c, e, categories)
	if len(rules.Results) == 1 && rules.Results[0].Confidence >= c.config.RuleThreshold {
		result := rules.Results[0]
		run.UsedClassifier = c.rules.Name()
		run.Reason = "A deterministic rule exceeded the high-confidence threshold."
		result.ClassifierName = c.Name()
		result.ClassifierVersion = c.Version()
		result.ScoreKind = "rule_confidence"
		result.InputHash = run.InputHash
		result.Timestamp = run.Timestamp
		run.Results = []domain.ClassificationResult{result}
		return run, nil
	}
	if c.semantic == nil {
		run.UsedClassifier = "none"
		run.Reason = "No high-confidence rule matched and local embeddings are not configured."
		return run, nil
	}
	semantic, err := c.semantic.Classify(ctx, e, categories)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	run.UsedClassifier = semantic.UsedClassifier
	run.Reason = semantic.Reason
	run.Candidates = semantic.Candidates
	for _, result := range semantic.Results {
		result.ClassifierName = c.Name()
		result.ClassifierVersion = c.Version()
		result.InputHash = run.InputHash
		result.Timestamp = run.Timestamp
		run.Results = append(run.Results, result)
	}
	return run, nil
}
