package classification

import (
	"context"
	"math"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
)

// Orthogonal components let tests set cosine scores independently of wording.
func cosineVector(score float64) []float32 {
	return []float32{float32(score), float32(math.Sqrt(1 - score*score))}
}
func hybridFixture(t *testing.T, first, second float64, top int) (*HybridClassifier, *fakeSemanticEmbeddings, []domain.EmailCategory) {
	t.Helper()
	f := &fakeSemanticEmbeddings{vector: []float32{1, 0}, categories: [][]float32{cosineVector(first), cosineVector(second)}}
	config := DefaultSemanticConfig()
	config.TopCandidates = top
	sem, err := NewSemanticClassifier(f, config)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewHybridClassifier(classifier(t), sem, DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	cats := []domain.EmailCategory{{ID: "a", Name: "Receipts", Description: "Paid purchases", Enabled: true}, {ID: "b", Name: "Newsletters", Description: "Editorial updates", Enabled: true}}
	return c, f, cats
}
func TestHybridEvidenceDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, subject, body, category, engine, reason string
		first, second                                 float64
	}{
		{"reinforce receipt below semantic threshold", "Your receipt", "Payment received. Order number: TEST.", "a", "HybridClassifier", "agree", .38, .1},
		{"reinforce weak newsletter", "Weekly digest", "Unsubscribe from our editorial issue.", "b", "HybridClassifier", "agree", .1, .38},
		{"semantic alone remains conservative", "An update", "Generic text.", "", "none", "minimum", .38, .1},
		{"semantic alone above gate", "An update", "Generic text.", "a", "SemanticClassifier", "clear lead", .6, .2},
		{"strong rule and inconclusive semantics", "Your receipt", "Payment received. Order number: TEST.", "a", "RuleBasedClassifier", "below", .2, .25},
		{"weak rule no support", "Weekly digest", "Unsubscribe from our editorial issue.", "", "none", "minimum", .1, .3},
		{"disagreement", "Your receipt", "Payment received. Order number: TEST.", "", "none", "disagree", .2, .65},
		{"near tie", "Your receipt", "Payment received. Order number: TEST.", "", "none", "too close", .60, .58},
		{"tie", "Your receipt", "Payment received. Order number: TEST.", "", "none", "too close", .6, .6},
		{"exclusion survives fallback", "Your receipt", "This is an estimate. Total paid: 10. Order number: TEST.", "", "none", "exclusion", .8, .1},
		{"competing rules survive fallback", "Your receipt and weekly digest", "Payment received. Order number: TEST. Unsubscribe.", "", "none", "competing", .8, .1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, cats := hybridFixture(t, tc.first, tc.second, 3)
			run, err := c.Classify(context.Background(), fixture(tc.subject, tc.body), cats)
			if err != nil {
				t.Fatal(err)
			}
			chosen := ""
			if len(run.Results) > 0 {
				r := run.Results[0]
				chosen = string(r.CategoryID)
				if r.ClassifierVersion != c.Version() || r.ClassifierName != c.Name() || r.InputHash != run.InputHash || !r.Timestamp.Equal(run.Timestamp) || len(r.Reasons) == 0 {
					t.Fatal("metadata lost", run)
				}
			}
			if chosen != tc.category || run.UsedClassifier != tc.engine || !strings.Contains(run.Reason, tc.reason) {
				t.Fatal(run)
			}
		})
	}
}
func TestStarterRulesWorkWithoutEmbeddings(t *testing.T) {
	c, err := NewHybridClassifier(classifier(t), nil, DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ subject, body, category string }{
		{"Application received", "We received your application for the support position.", "Job Applications"},
		{"Interview invitation", "Our hiring manager needs your availability to schedule.", "Job Interviews"},
		{"Your receipt", "Payment received. Order number: TEST.", "Receipts"},
		{"Weekly digest", "Unsubscribe.", ""},
		{"Application received and interview invitation", "We received your application for the position. Our hiring team needs availability to schedule.", ""},
	} {
		run, err := c.Classify(context.Background(), fixture(tc.subject, tc.body), testCategories())
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(run.Results) > 0 {
			got = string(run.Results[0].CategoryID)
			if run.Results[0].ScoreKind != "rule_confidence" {
				t.Fatal(run)
			}
		}
		if got != tc.category {
			t.Fatal(tc.subject, run)
		}
	}
}
func TestHybridCannotHideRunnerUpOrUseIncompleteBody(t *testing.T) {
	c, _, cats := hybridFixture(t, .60, .58, 1)
	e := fixture("Your receipt", "Payment received. Order number: TEST.")
	run, err := c.Classify(context.Background(), e, cats)
	if err != nil || len(run.Results) != 0 || !strings.Contains(run.Reason, "too close") {
		t.Fatal("top-1 presentation erased ambiguity", run, err)
	}
	e.BodyState = "unavailable"
	run, err = c.Classify(context.Background(), e, cats)
	if err != nil || len(run.Results) != 0 {
		t.Fatal("incomplete body classified", run, err)
	}
}
func TestHybridInferenceFailureAndCancellationAreNotAbstention(t *testing.T) {
	c, f, cats := hybridFixture(t, .6, .1, 3)
	f.err = embedding.ErrProvider
	if _, err := c.Classify(context.Background(), fixture("Your receipt", "Payment received. Order number: TEST."), cats); err != embedding.ErrProvider {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Classify(ctx, fixture("Subject", "Body"), cats); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestHybridPolicyValidationAndVersion(t *testing.T) {
	c, _, _ := hybridFixture(t, .6, .1, 3)
	for _, change := range []func(*HybridConfig){
		func(p *HybridConfig) { p.RuleThreshold = math.NaN() },
		func(p *HybridConfig) { p.ReinforcementRuleThreshold = p.RuleThreshold + .01 },
		func(p *HybridConfig) { p.ReinforcementSimilarity = 0 },
		func(p *HybridConfig) { p.ReinforcementSimilarity = .8 },
	} {
		p := DefaultHybridConfig()
		change(&p)
		if _, err := NewHybridClassifier(classifier(t), c.semantic, p); err != ErrInvalidConfig {
			t.Fatal("bad policy", p, err)
		}
	}
	p := DefaultHybridConfig()
	p.ReinforcementSimilarity = .36
	changed, err := NewHybridClassifier(classifier(t), c.semantic, p)
	if err != nil || changed.Version() == c.Version() {
		t.Fatal("policy not fingerprinted", err)
	}
}
func TestRuleConfigContentAndAlgorithmAreVersioned(t *testing.T) {
	original := classifier(t)
	cfg := StarterConfig()
	cfg.Rules[0].Confidence = .61 // Caller forgot to bump its label.
	changed, err := NewRuleBasedClassifier(cfg)
	if err != nil || changed.Version() == original.Version() {
		t.Fatal("rule edit retained classifier identity", err)
	}
	cfg.Rules[0].Confidence = .99
	if changed.config.Rules[0].Confidence != .61 {
		t.Fatal("caller mutated live rules")
	}
}
func TestRulesIgnoreQuotedAndSignatureEvidence(t *testing.T) {
	c := classifier(t)
	for _, body := range []string{
		"Please inspect the template.\n> Payment received. Order number: TEST.",
		"Please inspect the template.\nOn Tuesday someone wrote:\nPayment received. Order number: TEST.",
		"Please inspect the template.\n--\nPayment received. Order number: TEST.",
	} {
		run, err := c.Classify(context.Background(), fixture("Your receipt", body), testCategories())
		if err != nil || len(run.Results) != 0 {
			t.Fatal("old/quoted body became current evidence", run, err)
		}
	}
	e := fixture("Your receipt", "")
	html := "<p>Please inspect the template.</p><blockquote>Payment received. Order number: TEST.</blockquote>"
	e.BodyHTML = &html
	run, err := c.Classify(context.Background(), e, testCategories())
	if err != nil || len(run.Results) != 0 {
		t.Fatal("HTML quote became current evidence", run, err)
	}
}
func TestRulesRejectDuplicateCategoryIDs(t *testing.T) {
	cats := testCategories()
	cats[1].ID = cats[0].ID
	if _, err := classifier(t).Classify(context.Background(), fixture("Subject", "Body"), cats); err != ErrInvalidInput {
		t.Fatal(err)
	}
}

func TestPositiveRuleSignalsMustNotBeImmediatelyNegated(t *testing.T) {
	for _, tc := range []struct {
		text, phrase string
		want         bool
	}{
		{"unrelated to employment", "employment", false},
		{"no recruiter", "recruiter", false},
		{"not a job", "job", false},
		{"without employment", "employment", false},
		{"not for the hiring team", "hiring team", true}, // Not a general negation parser.
		{"no recruiter then a recruiter joined", "recruiter", true},
		{"your employment application", "employment", true},
	} {
		if got := phraseMatches(words(tc.text), words(tc.phrase), true); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	e := fixture("Application confirmation", "Your application for a product license is received. This request is not about employment.")
	run, err := classifier(t).Classify(context.Background(), e, testCategories())
	if err != nil || len(run.Results) != 0 {
		t.Fatal("negated keyword became positive evidence", run, err)
	}
	// Exclusions must still recognize negated phrases literally.
	if !phraseMatches("not received your application", "not received your application", false) {
		t.Fatal("exclusion lost")
	}
}
