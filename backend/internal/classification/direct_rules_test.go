package classification

import (
	"context"
	"testing"
)

func TestDirectRuleGateAndAbstention(t *testing.T) {
	c := classifier(t)
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
