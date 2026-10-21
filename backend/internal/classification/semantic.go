package classification

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

const SemanticVersion = "semantic-v3-close-category-suggestions"

//go:embed semantic_policy.json
var policyJSON []byte

type SemanticPolicy struct {
	MinimumAuthoredWords int     `json:"minimumAuthoredWords"`
	Version              string  `json:"version"`
	DatasetHash          string  `json:"datasetHash"`
	ModelVersion         string  `json:"modelVersion"`
	Fingerprint          string  `json:"fingerprint"`
	PreprocessingVersion string  `json:"preprocessingVersion"`
	MinimumScore         float64 `json:"minimumScore"`
	MinimumMargin        float64 `json:"minimumMargin"`
	SubjectWeight        float64 `json:"subjectWeight"`
	AuthoredWeight       float64 `json:"authoredWeight"`
	ForwardedWeight      float64 `json:"forwardedWeight"`
	QuotedWeight         float64 `json:"quotedWeight"`
	RuleThreshold        float64 `json:"ruleThreshold"`
}

func DefaultSemanticPolicy() SemanticPolicy {
	var p SemanticPolicy
	if json.Unmarshal(policyJSON, &p) != nil {
		panic("invalid semantic policy")
	}
	return p
}
func (p SemanticPolicy) Valid() bool {
	for _, v := range []float64{p.MinimumScore, p.MinimumMargin, p.SubjectWeight, p.AuthoredWeight, p.ForwardedWeight, p.QuotedWeight, p.RuleThreshold} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 2 {
			return false
		}
	}
	return p.MinimumAuthoredWords >= 0 && p.MinimumAuthoredWords <= 100 && p.Version == SemanticVersion && len(p.DatasetHash) == 64 && p.ModelVersion != "" && len(p.Fingerprint) == 64 && p.PreprocessingVersion != "" && p.MinimumScore > 0 && p.MinimumScore <= 1 && p.MinimumMargin <= 1 && p.AuthoredWeight > 0 && p.SubjectWeight <= p.AuthoredWeight && p.ForwardedWeight <= p.AuthoredWeight && p.QuotedWeight < p.AuthoredWeight
}

type SemanticClassifier struct {
	source  repository.SemanticRepository
	profile repository.IndexProfile
	policy  SemanticPolicy
	rules   ClassificationEngine
	version string
}

func NewSemanticClassifier(source repository.SemanticRepository, profile repository.IndexProfile, rules ClassificationEngine, policy SemanticPolicy) (*SemanticClassifier, error) {
	if source == nil || !policy.Valid() {
		return nil, ErrInvalidConfig
	}
	ruleVersion := "disabled"
	if rules != nil {
		ruleVersion = rules.Version()
	}
	data, _ := json.Marshal(struct {
		Algorithm string
		Policy    SemanticPolicy
		Rules     string
		Profile   repository.IndexProfile
	}{SemanticVersion, policy, ruleVersion, profile})
	sum := sha256.Sum256(data)
	return &SemanticClassifier{source: source, profile: profile, policy: policy, rules: rules, version: fmt.Sprintf("semantic-v3-%x", sum)}, nil
}
func (s *SemanticClassifier) Name() string                     { return "SharedSemanticClassifier" }
func (s *SemanticClassifier) Version() string                  { return s.version }
func (s *SemanticClassifier) Profile() repository.IndexProfile { return s.profile }
func (s *SemanticClassifier) Classify(ctx context.Context, e domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	snapshot, err := s.source.Snapshot(ctx, e.AccountID, e.ID, s.profile)
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	if snapshot.BaseHash != InputHash(e, categories) {
		return domain.ClassificationRun{}, repository.ErrConflict
	}
	return s.Assess(ctx, snapshot)
}

// Assess is pure computation over a copied, account-scoped compatible snapshot.
// All cosine comparisons use the exact same finite-vector kernel as search.
func (s *SemanticClassifier) Assess(ctx context.Context, snapshot repository.SemanticSnapshot) (domain.ClassificationRun, error) {
	if err := ctx.Err(); err != nil {
		return domain.ClassificationRun{}, err
	}
	e := snapshot.Email.Email
	evidence := &domain.SemanticEvidence{Version: SemanticVersion, Profile: repository.SemanticProfile(snapshot.Profile), Revision: snapshot.Revision, EmailRevision: snapshot.EmailRevision, BaseHash: snapshot.BaseHash, SourceRevision: snapshot.Email.ContentHash, ChunkGeneration: snapshot.Generation, State: "unclassified", Assigned: []domain.CategoryID{}, Scores: []domain.CategoryScore{}, MinimumScore: s.policy.MinimumScore, MinimumMargin: s.policy.MinimumMargin}
	run := domain.ClassificationRun{Semantic: evidence, AccountID: e.AccountID, EmailID: e.ID, ClassifierName: s.Name(), ClassifierVersion: s.Version(), InputHash: snapshot.Hash, Timestamp: time.Now().UTC().Truncate(time.Millisecond), UsedClassifier: "none", Reason: "At least two distinct passages, including authored or forwarded content, are required.", Results: []domain.ClassificationResult{}}
	assigned := []domain.CategoryID{}
	if snapshot.Preference.Mode == "unclassified" {
		run.UsedClassifier = "user"
		run.Reason = "User explicitly left this email unclassified."
		return run, nil
	}
	if snapshot.Preference.Mode == "assigned" {
		assigned = append(assigned, snapshot.Preference.CategoryID)
	} else {
		for _, c := range e.Categories {
			assigned = append(assigned, c.ID)
		}
	}
	if len(assigned) > 0 {
		sort.Slice(assigned, func(i, j int) bool { return assigned[i] < assigned[j] })
		evidence.Assigned = assigned
		evidence.State = "assigned"
		run.UsedClassifier = "user"
		run.Reason = "User assignments take priority; legacy assignments are preserved."
		if len(assigned) == 1 {
			run.Results = append(run.Results, semanticResult(run, assigned[0], 1, "user_assignment", []string{"Explicit user assignment; no semantic probability is claimed."}))
		}
		return run, nil
	}
	if snapshot.Availability == "ready" && snapshot.Profile.Model.Version == s.policy.ModelVersion && snapshot.Profile.Fingerprint == s.policy.Fingerprint && snapshot.Profile.PreprocessingVersion == s.policy.PreprocessingVersion {
		scores, err := Aggregate(ctx, snapshot.Chunks, snapshot.Prototypes, s.policy)
		if err != nil {
			return domain.ClassificationRun{}, err
		}
		evidence.Scores = scores
		for _, score := range scores[:min(5, len(scores))] {
			run.Candidates = append(run.Candidates, domain.ClassificationCandidate{CategoryID: score.CategoryID, Similarity: score.Score})
		}
	} else {
		run.Reason = "Compatible email/category vectors are unavailable; start or resume local indexing."
		if snapshot.Availability == "ready" {
			run.Reason = "This model or preprocessing version needs classifier evaluation."
		}
	}
	if s.rules != nil {
		ruleRun, err := s.rules.Classify(ctx, e, snapshot.Categories)
		if err != nil && !errors.Is(err, ErrInvalidInput) {
			return domain.ClassificationRun{}, err
		}
		if len(ruleRun.Results) == 1 && ruleRun.Results[0].Confidence >= s.policy.RuleThreshold {
			r := ruleRun.Results[0]
			evidence.State = "suggested"
			run.UsedClassifier = "rules"
			run.Reason = "A decisive, unambiguous deterministic rule takes priority."
			run.Results = append(run.Results, semanticResult(run, r.CategoryID, r.Confidence, "rule_confidence", r.Reasons))
			return run, nil
		}
		if ruleRun.Reason == "Specific rules match competing categories; no category chosen." {
			run.Reason = "Deterministic rules conflict; no primary category suggested."
			return run, nil
		}
	}
	if len(evidence.Scores) == 0 {
		return run, nil
	}
	best := evidence.Scores[0]
	if best.Score < s.policy.MinimumScore {
		run.Reason = "Semantic evidence is below the evaluated acceptance threshold."
		return run, nil
	}
	evidence.State = "suggested"
	run.UsedClassifier = "semantic"
	run.Reason = "Section-weighted passages support one category."
	for _, score := range SuggestedScores(evidence.Scores, s.policy.MinimumScore, s.policy.MinimumMargin) {
		run.Results = append(run.Results, semanticResult(run, score.CategoryID, score.Score, "cosine_similarity", []string{"Normalized section means combine subject and body evidence; forwarded content is primary when no authored body exists, and quotations have lower weight.", "Cosine similarity is an evidence score, not a calibrated probability."}))
	}
	if len(run.Results) > 1 {
		run.Reason = "Several categories are within the lead threshold of the best score; all are suggested for review."
	}
	return run, nil
}

// SuggestedScores retains every category within the inclusive lead of the best,
// provided each independently clears the minimum evidence score. Input is already
// ranked by score then category ID. The tolerance handles floating-point boundary
// subtraction, not a widening of the user-selected margin.
func SuggestedScores(scores []domain.CategoryScore, minimum, lead float64) []domain.CategoryScore {
	if len(scores) == 0 || scores[0].Score < minimum {
		return nil
	}
	n := 0
	for _, score := range scores {
		if score.Score < minimum || scores[0].Score-score.Score > lead+1e-12 {
			break
		}
		n++
	}
	return scores[:n]
}
func semanticResult(run domain.ClassificationRun, id domain.CategoryID, score float64, kind string, reasons []string) domain.ClassificationResult {
	return domain.ClassificationResult{AccountID: run.AccountID, EmailID: run.EmailID, CategoryID: id, Confidence: score, ScoreKind: kind, ClassifierName: run.ClassifierName, ClassifierVersion: run.ClassifierVersion, InputHash: run.InputHash, Timestamp: run.Timestamp, Reasons: reasons}
}

type weightedChunk struct {
	source repository.VectorSource
	weight float64
}

// Each section has a fixed total weight. Its nonoverlapping unique chunks share
// that weight, so adding duplicate/overlapping/quoted chunks cannot add votes.
func Aggregate(ctx context.Context, chunks []repository.VectorSource, prototypes []repository.SemanticPrototype, p SemanticPolicy) ([]domain.CategoryScore, error) {
	if len(chunks) > 4096 || len(prototypes) > repository.MaxSemanticCategories {
		return nil, ErrInvalidInput
	}
	groups := map[string][]repository.VectorSource{}
	seen := map[string]bool{}
	ordered := append([]repository.VectorSource{}, chunks...)
	priority := func(kind string) int {
		switch kind {
		case "authored":
			return 0
		case "subject":
			return 1
		case "forwarded":
			return 2
		case "quoted":
			return 3
		default:
			return 4
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i].Chunk, ordered[j].Chunk
		if priority(a.SectionType) != priority(b.SectionType) {
			return priority(a.SectionType) < priority(b.SectionType)
		}
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		return a.ID < b.ID
	})
	selected := []repository.VectorSource{}
	for _, c := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.Chunk.SectionType == "signature" || seen[c.Chunk.ContentHash] {
			continue
		}
		weight := sectionWeight(c.Chunk.SectionType, p)
		if weight == 0 {
			continue
		}
		overlap := false
		for _, old := range selected {
			a, b := c.Chunk, old.Chunk
			if a.SourceKind != "" && a.SourceKind == b.SourceKind && a.SectionIndex == b.SectionIndex && a.StartOffset < b.EndOffset && b.StartOffset < a.EndOffset {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		seen[c.Chunk.ContentHash] = true
		selected = append(selected, c)
		groups[c.Chunk.SectionType] = append(groups[c.Chunk.SectionType], c)
	}
	// Forwarding without a covering note is a complete message, not a missing
	// authored body. Its forwarded passages supply the primary body evidence.
	// Keep quoted-only, subject-only and one-passage messages ineligible.
	primary := append([]repository.VectorSource{}, groups["authored"]...)
	primary = append(primary, groups["body"]...)
	forwardedPrimary := len(primary) == 0 && len(groups["forwarded"]) > 0
	if forwardedPrimary {
		primary = groups["forwarded"]
	}
	words := 0
	for _, c := range primary {
		words += len(strings.Fields(c.Chunk.Text))
	}
	if len(selected) < 2 || len(primary) == 0 || words < p.MinimumAuthoredWords {
		return []domain.CategoryScore{}, nil
	}
	weightFor := func(kind string) float64 {
		if kind == "forwarded" && forwardedPrimary {
			return p.AuthoredWeight
		}
		return sectionWeight(kind, p)
	}
	total := 0.0
	for _, group := range []string{"subject", "authored", "body", "forwarded", "quoted"} {
		if len(groups[group]) > 0 {
			total += weightFor(group)
		}
	}
	weighted := []weightedChunk{}
	for _, c := range selected {
		w := weightFor(c.Chunk.SectionType) / total / float64(len(groups[c.Chunk.SectionType]))
		weighted = append(weighted, weightedChunk{c, w})
	}
	scores := []domain.CategoryScore{}
	for _, prototype := range prototypes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(prototype.Vector) == 0 {
			return nil, ErrInvalidInput
		}
		score := domain.CategoryScore{CategoryID: prototype.Category.ID, PrototypeHash: prototype.SourceHash, PrototypeStatus: prototype.Status, ChunkCount: len(weighted), Evidence: []domain.ClassificationChunkEvidence{}}
		for _, c := range weighted {
			sim, err := retrieval.Cosine(c.source.Vector, prototype.Vector)
			if err != nil {
				return nil, ErrInvalidInput
			}
			score.Score += sim * c.weight
			score.Evidence = append(score.Evidence, domain.ClassificationChunkEvidence{ChunkID: c.source.Chunk.ID, Section: c.source.Chunk.SectionType, Similarity: sim, Weight: c.weight})
		}
		score.Score = math.Max(-1, math.Min(1, score.Score))
		sort.Slice(score.Evidence, func(i, j int) bool {
			a, b := score.Evidence[i], score.Evidence[j]
			if a.Weight*a.Similarity != b.Weight*b.Similarity {
				return a.Weight*a.Similarity > b.Weight*b.Similarity
			}
			return a.ChunkID < b.ChunkID
		})
		score.Evidence = score.Evidence[:min(4, len(score.Evidence))]
		scores = append(scores, score)
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return scores[i].CategoryID < scores[j].CategoryID
	})
	return scores, nil
}
func sectionWeight(kind string, p SemanticPolicy) float64 {
	switch kind {
	case "subject":
		return p.SubjectWeight
	case "authored", "body":
		return p.AuthoredWeight
	case "forwarded":
		return p.ForwardedWeight
	case "quoted":
		return p.QuotedWeight
	default:
		return 0
	}
}

// SemanticRules adapts only the new classifier's rules to the combined Jobs
// default. The existing rule engine/version and historical rule records stay intact.
func SemanticRules() (*RuleBasedClassifier, error) {
	config := StarterConfig()
	config.Version = "semantic-defaults-v1"
	for i := range config.Rules {
		if config.Rules[i].CategoryName == "Job Applications" || config.Rules[i].CategoryName == "Job Interviews" {
			config.Rules[i].CategoryName = "Jobs"
		}
	}
	return NewRuleBasedClassifier(config)
}

// FreshInput preserves the old fingerprint branch for historical rule records.
func FreshInput(run domain.ClassificationRun, e domain.Email, categories []domain.EmailCategory) bool {
	if run.Semantic != nil {
		return run.Semantic.BaseHash == InputHash(e, categories)
	}
	return run.InputHash == InputHash(e, categories)
}
