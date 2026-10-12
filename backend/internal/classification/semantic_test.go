package classification

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
)

type fakeSemanticEmbeddings struct {
	vector     []float32
	categories [][]float32
	calls      int
	err        error
	seen       []domain.EmailCategory
}

func (f *fakeSemanticEmbeddings) ProviderName() string { return "fake/local-v1" }
func (f *fakeSemanticEmbeddings) Dimension() int       { return 2 }
func (f *fakeSemanticEmbeddings) EmbedEmail(ctx context.Context, a domain.AccountID, id domain.EmailID) ([]float32, error) {
	f.calls++
	return f.vector, f.err
}
func (f *fakeSemanticEmbeddings) EmbedCategories(ctx context.Context, a domain.AccountID, c []domain.EmailCategory) ([][]float32, error) {
	f.seen = c
	return f.categories, f.err
}
func semanticFixture(t *testing.T, f *fakeSemanticEmbeddings) *SemanticClassifier {
	t.Helper()
	c, err := NewSemanticClassifier(f, DefaultSemanticConfig())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func semanticCategories() []domain.EmailCategory {
	return []domain.EmailCategory{{ID: "a", Name: "A", Description: "First", Enabled: true}, {ID: "b", Name: "B", Description: "Second", Enabled: true}, {ID: "disabled", Description: "Disabled", Enabled: false}}
}
func TestSemanticRankingAndAbstention(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vector     []float32
		categories [][]float32
		chosen     string
	}{
		{"select", []float32{3, 0}, [][]float32{{4, 0}, {0, 4}}, "a"},
		{"reverse", []float32{0, 1}, [][]float32{{1, 0}, {0, 1}}, "b"},
		{"tie", []float32{1, 1}, [][]float32{{1, 0}, {0, 1}}, ""},
		{"negative", []float32{-1, -1}, [][]float32{{1, 0}, {0, 1}}, ""},
		{"near tie", []float32{1, 0}, [][]float32{{1, 0}, {1, .1}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSemanticEmbeddings{vector: tc.vector, categories: tc.categories}
			c := semanticFixture(t, f)
			run, err := c.Classify(context.Background(), fixture("Subject", "Body"), semanticCategories())
			if err != nil {
				t.Fatal(err)
			}
			chosen := ""
			if len(run.Results) > 0 {
				chosen = string(run.Results[0].CategoryID)
				if run.Results[0].ScoreKind != "cosine_similarity" {
					t.Fatal("score semantics lost")
				}
			}
			if chosen != tc.chosen || len(run.Candidates) != 2 || len(f.seen) != 2 || run.Reason == "" {
				t.Fatal(run)
			}
			if tc.name == "tie" && run.Candidates[0].CategoryID != "a" {
				t.Fatal("unstable tie ordering")
			}
		})
	}
}
func TestSemanticFailuresBoundsAndCancellation(t *testing.T) {
	f := &fakeSemanticEmbeddings{vector: []float32{1, 0}, categories: [][]float32{{1, 0}, {0, 1}}}
	c := semanticFixture(t, f)
	e := fixture("Subject", "Body")
	e.BodyState = "unavailable"
	run, err := c.Classify(context.Background(), e, semanticCategories())
	if err != nil || len(run.Results) != 0 || f.calls != 0 {
		t.Fatal(run, err)
	}
	e.BodyState = "complete"
	f.categories = [][]float32{{1, 0}}
	if _, err := c.Classify(context.Background(), e, semanticCategories()); err != embedding.ErrVector {
		t.Fatal(err)
	}
	f.categories = [][]float32{{1, 0}, {float32(math.NaN()), 1}}
	if _, err := c.Classify(context.Background(), e, semanticCategories()); err != embedding.ErrVector {
		t.Fatal(err)
	}
	f.err = embedding.ErrProvider
	if _, err := c.Classify(context.Background(), e, semanticCategories()); err != embedding.ErrProvider {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Classify(ctx, e, semanticCategories()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, policy := range []SemanticConfig{{0, .05, 3}, {.4, -1, 3}, {.4, .05, 0}, {math.NaN(), 0, 3}} {
		if _, err := NewSemanticClassifier(f, policy); err != ErrInvalidConfig {
			t.Fatal(err)
		}
	}
}
func TestCosineSimilarity(t *testing.T) {
	for _, tc := range []struct {
		a, b []float32
		want float64
	}{{[]float32{2, 0}, []float32{9, 0}, 1}, {[]float32{1, 0}, []float32{0, 1}, 0}, {[]float32{1, 0}, []float32{-1, 0}, -1}} {
		got, err := CosineSimilarity(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	for _, bad := range [][]float32{{}, {0, 0}, {1}, {float32(math.Inf(1)), 0}} {
		if _, err := CosineSimilarity([]float32{1, 0}, bad); err != embedding.ErrVector {
			t.Fatal(err)
		}
	}
}
func TestHybridRulePrecedenceAndSemanticFallback(t *testing.T) {
	config := StarterConfig()
	config.Version = "strong-fixture"
	for i := range config.Rules {
		config.Rules[i].Confidence = .95
	}
	rules, err := NewRuleBasedClassifier(config)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSemanticEmbeddings{vector: []float32{0, 1}, categories: [][]float32{{1, 0}, {0, 1}}}
	semantic := semanticFixture(t, f)
	cats := []domain.EmailCategory{{ID: "a", Name: "Receipts", Description: "Purchases", Enabled: true}, {ID: "b", Name: "Personal", Description: "Family", Enabled: true}}
	hybrid, err := NewHybridClassifier(rules, semantic, DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	run, err := hybrid.Classify(context.Background(), fixture("Your receipt", "Payment received. Order number: TEST."), cats)
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "a" || run.UsedClassifier != "RuleBasedClassifier" || f.calls != 0 {
		t.Fatal(run, err)
	}
	run, err = hybrid.Classify(context.Background(), fixture("Lunch plans", "See you soon."), cats)
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "b" || run.UsedClassifier != "SemanticClassifier" || len(run.Candidates) != 2 {
		t.Fatal(run, err)
	}
	weak, err := NewHybridClassifier(classifier(t), semantic, DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	run, err = weak.Classify(context.Background(), fixture("Your receipt", "Payment received. Order number: TEST."), cats)
	if err != nil || run.UsedClassifier != "SemanticClassifier" {
		t.Fatal("weak rule won", run, err)
	}
	none, _ := NewHybridClassifier(rules, nil, DefaultHybridConfig())
	run, err = none.Classify(context.Background(), fixture("Unrelated", "Body"), cats)
	if err != nil || len(run.Results) != 0 || run.UsedClassifier != "none" || run.Reason == "" {
		t.Fatal(run, err)
	}
	if hybrid.Version() == none.Version() || !reflect.DeepEqual(run.Candidates, []domain.ClassificationCandidate(nil)) {
		t.Fatal("version or abstention")
	}
}
