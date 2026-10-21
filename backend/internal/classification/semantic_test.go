package classification

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding/model"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

func geometryChunk(id, kind string, x, y float32, index int) repository.VectorSource {
	return repository.VectorSource{Chunk: repository.EmailChunk{ID: id, ChunkInput: repository.ChunkInput{Index: index, SectionType: kind, Text: id, ContentHash: id, SourceKind: kind, SectionIndex: 0, StartOffset: index * 20, EndOffset: index*20 + 10}}, Vector: []float32{x, y}}
}
func geometryPrototypes() []repository.SemanticPrototype {
	return []repository.SemanticPrototype{{Category: domain.EmailCategory{ID: "jobs", Name: "Jobs", Enabled: true}, SourceHash: "j", Status: "ready", Vector: []float32{0, 1}}, {Category: domain.EmailCategory{ID: "finance", Name: "Finance", Enabled: true}, SourceHash: "f", Status: "ready", Vector: []float32{1, 0}}}
}
func geometrySnapshot(chunks []repository.VectorSource) repository.SemanticSnapshot {
	p := DefaultSemanticPolicy()
	profile := repository.IndexProfile{Model: repository.EmbeddingModel{ID: "model", Version: p.ModelVersion, Dimension: 2}, PreprocessingVersion: p.PreprocessingVersion, Fingerprint: p.Fingerprint, CategoryRecipe: repository.CategoryRecipe}
	cats := []domain.EmailCategory{{ID: "jobs", Name: "Jobs", Enabled: true}, {ID: "finance", Name: "Finance", Enabled: true}}
	return repository.SemanticSnapshot{Email: repository.StoredEmail{Email: domain.Email{ID: "e", AccountID: "a"}, ContentHash: "source"}, Profile: profile, Availability: "ready", Prototypes: geometryPrototypes(), Categories: cats, Chunks: chunks, Hash: "snapshot"}
}
func TestSemanticAggregationNormalizesLengthAndDeduplicates(t *testing.T) {
	p := DefaultSemanticPolicy()
	ctx := context.Background()
	base := []repository.VectorSource{geometryChunk("subject", "subject", 0, 1, 0), geometryChunk("authored", "authored", 0, 1, 1), geometryChunk("quote", "quoted", 1, 0, 2)}
	scores, err := Aggregate(ctx, base, geometryPrototypes(), p)
	if err != nil || scores[0].CategoryID != "jobs" {
		t.Fatal(scores, err)
	}
	expanded := append([]repository.VectorSource{}, base...)
	for i := 3; i < 203; i++ {
		expanded = append(expanded, geometryChunk(fmt.Sprintf("quote-%d", i), "quoted", 1, 0, i))
	}
	long, err := Aggregate(ctx, expanded, geometryPrototypes(), p)
	if err != nil || math.Abs(long[0].Score-scores[0].Score) > 1e-12 {
		t.Fatal("quoted length inflated score", long, err)
	}
	duplicate := base[1]
	duplicate.Chunk.ID = "copy"
	expanded = append(base, duplicate)
	repeated, _ := Aggregate(ctx, expanded, geometryPrototypes(), p)
	if repeated[0].Score != scores[0].Score || repeated[0].ChunkCount != scores[0].ChunkCount {
		t.Fatal("duplicate counted twice")
	}
	overlapping := base[1]
	overlapping.Chunk.ID = "overlap"
	overlapping.Chunk.ContentHash = "other"
	overlapping.Chunk.StartOffset++
	overlapping.Chunk.EndOffset++
	repeated, _ = Aggregate(ctx, append(base, overlapping), geometryPrototypes(), p)
	if repeated[0].ChunkCount != 3 {
		t.Fatal("overlap counted twice")
	}
	// One perfect outlier cannot beat consistent contrary subject/body evidence.
	outlier := []repository.VectorSource{geometryChunk("s", "subject", 1, 0, 0), geometryChunk("a", "authored", 1, 0, 1), geometryChunk("b", "authored", 1, 0, 2), geometryChunk("perfect", "authored", 0, 1, 3)}
	ranked, _ := Aggregate(ctx, outlier, geometryPrototypes(), p)
	if ranked[0].CategoryID != "finance" || ranked[0].Score >= 1 {
		t.Fatal("single highest chunk controlled classification", ranked)
	}
}
func TestSemanticAbstentionOverridesAndDeterministicTies(t *testing.T) {
	ctx := context.Background()
	s := &SemanticClassifier{policy: DefaultSemanticPolicy(), version: "semantic-test"}
	chunks := []repository.VectorSource{geometryChunk("s", "subject", 0, 1, 0), geometryChunk("a", "authored", 0, 1, 1)}
	snapshot := geometrySnapshot(chunks)
	run, err := s.Assess(ctx, snapshot)
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "jobs" || run.Results[0].ScoreKind != "cosine_similarity" || len(run.Semantic.Scores[0].Evidence) != 2 {
		t.Fatal(run, err)
	}
	snapshot.Prototypes[1].Vector = []float32{0, 1}
	run, _ = s.Assess(ctx, snapshot)
	if len(run.Results) != 2 || run.Semantic.State != "suggested" || run.Results[0].CategoryID != "finance" || run.Results[1].CategoryID != "jobs" {
		t.Fatal("tied categories not deterministically suggested", run)
	}
	snapshot.Preference = domain.CategoryPreference{Mode: "assigned", CategoryID: "finance"}
	run, _ = s.Assess(ctx, snapshot)
	if len(run.Results) != 1 || run.Results[0].CategoryID != "finance" || run.Results[0].ScoreKind != "user_assignment" || run.Semantic.State != "assigned" {
		t.Fatal("override lost", run)
	}
	snapshot.Preference.Mode = "unclassified"
	run, _ = s.Assess(ctx, snapshot)
	if len(run.Results) != 0 || run.UsedClassifier != "user" {
		t.Fatal("user abstention ignored")
	}
	snapshot.Preference = domain.CategoryPreference{}
	snapshot.Email.Email.Categories = []domain.EmailCategory{{ID: "jobs"}, {ID: "finance"}}
	run, _ = s.Assess(ctx, snapshot)
	if run.Semantic.State != "assigned" || len(run.Results) != 0 || len(snapshot.Email.Email.Categories) != 2 {
		t.Fatal("legacy assignments collapsed")
	}
	for _, kinds := range [][]string{{"subject"}, {"subject", "signature"}, {"subject", "quoted"}} {
		list := []repository.VectorSource{}
		for i, k := range kinds {
			list = append(list, geometryChunk(fmt.Sprint(i), k, 0, 1, i))
		}
		scores, err := Aggregate(ctx, list, geometryPrototypes(), s.policy)
		if err != nil || len(scores) != 0 {
			t.Fatal("insufficient primary text accepted", kinds)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.Assess(cancelled, geometrySnapshot(chunks)); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}

func TestSemanticCloseCategorySelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scores []float64
		want   int
	}{
		{"clear winner", []float64{.52, .48}, 1},
		{"close runners", []float64{.52, .50, .46}, 2},
		{"inclusive boundary", []float64{.60, .57, .5699}, 2},
		{"floor still applies", []float64{.22, .20, .199}, 2},
		{"weak best", []float64{.19, .18}, 0},
		{"all tied", []float64{.5, .5, .5, .5, .5, .5}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := []domain.CategoryScore{}
			for i, v := range tc.scores {
				list = append(list, domain.CategoryScore{CategoryID: domain.CategoryID(fmt.Sprint(i)), Score: v})
			}
			if got := SuggestedScores(list, .20, .03); len(got) != tc.want {
				t.Fatal(got)
			}
		})
	}
	if DefaultSemanticPolicy().MinimumMargin != .03 {
		t.Fatal("requested lead threshold not installed")
	}
}

func TestSemanticForwardedBodySuppliesPrimaryEvidence(t *testing.T) {
	ctx := context.Background()
	p := DefaultSemanticPolicy()
	// The subject favors finance, but the complete forwarded message favors jobs.
	// Giving that body primary weight must match the original authored message.
	authored := []repository.VectorSource{geometryChunk("s", "subject", 1, 0, 0), geometryChunk("f1", "authored", 0, 1, 1), geometryChunk("f2", "authored", 0, 1, 2)}
	original, err := Aggregate(ctx, authored, geometryPrototypes(), p)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := append([]repository.VectorSource{}, authored...)
	for i := 1; i < len(forwarded); i++ {
		forwarded[i].Chunk.SectionType = "forwarded"
		forwarded[i].Chunk.SourceKind = "forwarded"
	}
	scores, err := Aggregate(ctx, forwarded, geometryPrototypes(), p)
	if err != nil || len(scores) != 2 || scores[0].CategoryID != "jobs" || scores[0].Score != original[0].Score || scores[0].ChunkCount != 3 {
		t.Fatal("forwarded-only body lost or underweighted", scores, err)
	}
	engine := &SemanticClassifier{policy: p, version: "test"}
	run, err := engine.Assess(ctx, geometrySnapshot(forwarded))
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "jobs" {
		t.Fatal("forwarded body did not produce a semantic suggestion", run, err)
	}
	// An actual authored reply keeps priority over conflicting forwarded history.
	withReply := append(forwarded, geometryChunk("reply", "authored", 1, 0, 3))
	scores, err = Aggregate(ctx, withReply, geometryPrototypes(), p)
	if err != nil || scores[0].CategoryID != "finance" {
		t.Fatal("forwarded history overrode authored text", scores, err)
	}
	// Repeating forwarded material cannot create more votes or evidence passages.
	repeated := append([]repository.VectorSource{}, forwarded...)
	for i := 3; i < 203; i++ {
		repeated = append(repeated, geometryChunk(fmt.Sprint(i), "forwarded", 0, 1, i))
	}
	scores, err = Aggregate(ctx, repeated, geometryPrototypes(), p)
	if err != nil || math.Abs(scores[0].Score-original[0].Score) > 1e-12 {
		t.Fatal("forwarded length inflated score", scores, err)
	}
	for _, chunks := range [][]repository.VectorSource{forwarded[:1], forwarded[1:2]} {
		scores, err = Aggregate(ctx, chunks, geometryPrototypes(), p)
		if err != nil || len(scores) != 0 {
			t.Fatal("single passage accepted", scores, err)
		}
	}
}
func TestSemanticMissingVectorsModelChangeAndRulePriority(t *testing.T) {
	ctx := context.Background()
	policy := DefaultSemanticPolicy()
	rules, err := SemanticRules()
	if err != nil {
		t.Fatal(err)
	}
	s := &SemanticClassifier{policy: policy, version: "semantic-test", rules: rules}
	snapshot := geometrySnapshot([]repository.VectorSource{geometryChunk("s", "subject", 1, 0, 0), geometryChunk("a", "authored", 1, 0, 1)})
	snapshot.Email.Email.ProviderID = "provider"
	snapshot.Email.Email.ReceivedAt = time.Unix(1700000000, 0)
	snapshot.Email.Email.BodyState = "complete"
	snapshot.Email.Email.Subject = "Your receipt"
	snapshot.Email.Email.BodyText = "Payment received. Order number: AX-24."
	snapshot.Categories = append(snapshot.Categories, domain.EmailCategory{ID: "receipts", Name: "Receipts", Enabled: true})
	run, err := s.Assess(ctx, snapshot)
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "receipts" || run.Results[0].ScoreKind != "rule_confidence" {
		t.Fatal("decisive rule did not take priority", run, err)
	}
	s.rules = nil
	snapshot.Availability = "email_vectors_unavailable"
	run, _ = s.Assess(ctx, snapshot)
	if len(run.Results) != 0 {
		t.Fatal("missing vectors classified")
	}
	snapshot.Availability = "ready"
	snapshot.Profile.Model.Version = "changed"
	run, _ = s.Assess(ctx, snapshot)
	if len(run.Results) != 0 {
		t.Fatal("untuned model reused thresholds")
	}
}
func TestSemanticPolicyPinsDatasetAndModel(t *testing.T) {
	p := DefaultSemanticPolicy()
	data, err := os.ReadFile("testdata/semantic-eval-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid() || p.DatasetHash != fmt.Sprintf("%x", sha256.Sum256(data)) || p.ModelVersion != model.Identity().Version || p.Fingerprint != model.Identity().PreprocessingFingerprint || p.PreprocessingVersion != mailtext.PreprocessingVersion {
		t.Fatal("unqualified semantic policy")
	}
}

func TestSemanticExplicitRuleConflictAbstains(t *testing.T) {
	config := RuleConfig{Name: "ConflictRules", Version: "test", AmbiguityMargin: .15, Rules: []Rule{{ID: "jobs", CategoryName: "Jobs", Confidence: .9, All: []Condition{{Field: "subject", AnyOf: []string{"project"}, Reason: "Specific project signal."}}}, {ID: "finance", CategoryName: "Finance", Confidence: .9, All: []Condition{{Field: "subject", AnyOf: []string{"project"}, Reason: "Competing project signal."}}}}}
	rules, err := NewRuleBasedClassifier(config)
	if err != nil {
		t.Fatal(err)
	}
	engine := &SemanticClassifier{rules: rules, policy: DefaultSemanticPolicy(), version: "test"}
	snapshot := geometrySnapshot([]repository.VectorSource{geometryChunk("s", "subject", 0, 1, 0), geometryChunk("a", "authored", 0, 1, 1)})
	snapshot.Email.Email.ProviderID = "p"
	snapshot.Email.Email.ReceivedAt = time.Unix(1700000000, 0)
	snapshot.Email.Email.Subject = "Project"
	run, err := engine.Assess(context.Background(), snapshot)
	if err != nil || len(run.Results) != 0 {
		t.Fatal("semantic scores bypassed rule conflict", run, err)
	}
}
