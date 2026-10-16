package classification

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
)

func classifier(t *testing.T) *RuleBasedClassifier {
	t.Helper()
	engine, err := NewRuleBasedClassifier(StarterConfig())
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
func testCategories() []domain.EmailCategory {
	result := []domain.EmailCategory{}
	for _, name := range []string{"Job Applications", "Job Interviews", "Receipts", "Newsletters", "School"} {
		result = append(result, domain.EmailCategory{ID: domain.CategoryID(name), Name: name, Description: "Synthetic category description", Enabled: true})
	}
	return result
}
func fixture(subject, body string) domain.Email {
	return domain.Email{ID: "mail", AccountID: "account", ProviderID: "provider-mail", Sender: domain.EmailAddress{Address: "sender@example.test"}, Subject: subject, BodyText: body, BodyState: "complete", ReceivedAt: time.Now()}
}
func TestStarterRulesAndFalsePositives(t *testing.T) {
	engine := classifier(t)
	tests := []struct{ name, subject, body, category string }{
		{"application", "Application received", "We received your application for the engineering position.", "Job Applications"},
		{"interview", "Interview invitation", "Our hiring team would like your availability to schedule a meeting.", "Job Interviews"},
		{"receipt", "Your receipt", "Payment received. Order number: SYNTHETIC-123.", "Receipts"},
		{"digest", "Weekly digest", "In this issue: campus news. Manage your subscription or unsubscribe.", ""},
		{"case and punctuation", "PAYMENT CONFIRMATION", "TOTAL PAID: $12.00; TRANSACTION-ID: fixture.", "Receipts"},
		{"unrelated", "Saturday plans", "Would you like to have lunch?", ""},
		{"single job keyword", "Job opportunities", "A few new jobs and positions are open.", ""},
		{"software application", "Application received", "Your application for the software license was received.", ""},
		{"school application", "Application received", "Your application for college admissions is complete.", ""},
		{"loan application", "Application confirmation", "Your application for a loan application was received; position details are optional.", ""},
		{"negated application", "Application received", "We have not received your application for the position.", ""},
		{"application marketing", "Thank you for applying", "Your application for the position: read our newsletter and unsubscribe below.", ""},
		{"application advice", "Job application tips", "Your application for the position should be clear.", ""},
		{"interview keyword only", "Interview", "Let us know your availability.", ""},
		{"research interview", "Interview invitation", "You are a candidate for a research interview. Send availability to schedule.", ""},
		{"podcast", "Podcast interview with a recruiter", "The recruiter has availability to schedule.", ""},
		{"interview advice", "Interview questions", "A hiring manager suggests questions for your calendar.", ""},
		{"mock interview", "Interview invitation", "Our hiring team will schedule a mock interview.", ""},
		{"unrelated author", "Interview with an author", "Here is a meeting link and calendar invitation.", ""},
		{"receipt keyword", "Receipt", "Please give us a receipt.", ""},
		{"receipt without transaction", "Your receipt", "Thank you for browsing our store.", ""},
		{"unpaid order", "Order confirmation", "Order number: 12. Payment is pending.", ""},
		{"failed payment", "Payment confirmation", "Payment has failed. Amount paid: $0. Order number: 12.", ""},
		{"sample receipt", "Your receipt template", "Sample receipt. Total paid: $10. Order number: 12.", ""},
		{"estimate", "Your receipt", "This is an estimate. Total paid will be shown under order number 12.", ""},
		{"unsubscribe alone", "Account alert", "Unsubscribe from alerts. Your password was reset.", ""},
		{"digest without footer", "Weekly digest", "Messages from your friend.", ""},
		{"newsletter template", "Newsletter design", "Manage preferences for the newsletter template.", ""},
		{"replied", "Re: Application received", "We received your application for the position.", ""},
		{"forwarded", "Fwd: Your receipt", "Payment received. Order number: 123.", ""},
		{"word boundaries", "Your receiptable payment confirmationX", "Unpaid with a card. Reorder number is pending.", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, err := engine.Classify(context.Background(), fixture(test.subject, test.body), testCategories())
			if err != nil {
				t.Fatal(err)
			}
			if test.category == "" {
				if len(run.Results) != 0 {
					t.Fatalf("false positive: %v", run.Results[0].CategoryID)
				}
				return
			}
			if len(run.Results) != 1 || string(run.Results[0].CategoryID) != test.category {
				t.Fatalf("expected %s, got %v", test.category, run.Results)
			}
			result := run.Results[0]
			if result.Confidence <= 0 || result.Confidence > .65 || len(result.Reasons) < 2 || result.Timestamp.IsZero() || result.EmailID != "mail" || result.ClassifierName != "RuleBasedClassifier" || result.InputHash != run.InputHash {
				t.Fatal("incomplete or overconfident prediction")
			}
			for _, reason := range result.Reasons {
				if strings.TrimSpace(reason) == "" {
					t.Fatal("missing reason")
				}
			}
		})
	}
}
func TestAbstentionAndLabelRules(t *testing.T) {
	engine := classifier(t)
	email := fixture("Application received and interview invitation", "We received your application for the position. Our hiring team will schedule using your availability.")
	run, err := engine.Classify(context.Background(), email, testCategories())
	if err != nil || len(run.Results) != 0 {
		t.Fatal("ambiguous categories must abstain", err)
	}
	email = fixture("This week's update", "In this issue: news. Unsubscribe here.")
	email.Labels = []domain.EmailLabel{{AccountID: email.AccountID, ProviderID: "opaque", Name: "Newsletters", Kind: domain.LabelKindUser}}
	run, err = engine.Classify(context.Background(), email, testCategories())
	if err != nil || len(run.Results) != 1 || run.Results[0].CategoryID != "Newsletters" {
		t.Fatal("label rule", err)
	}
	categories := testCategories()
	categories[3].Enabled = false
	run, _ = engine.Classify(context.Background(), email, categories)
	if len(run.Results) != 0 {
		t.Fatal("disabled category predicted")
	}
	categories = testCategories()
	categories = append(categories, domain.EmailCategory{ID: "duplicate", Name: "newsletters", Enabled: true})
	run, _ = engine.Classify(context.Background(), email, categories)
	if len(run.Results) != 0 {
		t.Fatal("ambiguous category definition selected")
	}
	for _, state := range []string{"", "unavailable"} {
		email.BodyState = state
		run, _ = engine.Classify(context.Background(), email, testCategories())
		if len(run.Results) != 0 {
			t.Fatal("missing cached body treated as evidence")
		}
	}
	email.BodyState = "complete"
	email.Labels[0].AccountID = "other"
	if _, err = engine.Classify(context.Background(), email, testCategories()); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("foreign label accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = engine.Classify(ctx, fixture("", ""), testCategories()); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation")
	}
}
func TestConfigurableSenderAndDomainSignals(t *testing.T) {
	config := RuleConfig{Name: "test", Version: "1", Rules: []Rule{{ID: "exact-sender", CategoryName: "School", Confidence: .65, All: []Condition{
		{Field: "sender", AnyOf: []string{"office@school.example.test"}, Reason: "The sender address matches the configured school office."},
		{Field: "sender_domain", AnyOf: []string{"school.example.test"}, Reason: "The sender domain exactly matches the configured school domain."},
	}}}}
	engine, err := NewRuleBasedClassifier(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Rules[0].All[0].AnyOf[0] = "attacker@example.test" // Constructor must own its configuration.
	email := fixture("Notice", "")
	email.Sender.Address = "OFFICE@SCHOOL.EXAMPLE.TEST"
	run, _ := engine.Classify(context.Background(), email, testCategories())
	if len(run.Results) != 1 {
		t.Fatal("exact sender/domain rule failed")
	}
	for _, address := range []string{"office@school.example.test.evil.test", "office@sub.school.example.test", "not-an-address"} {
		email.Sender.Address = address
		email.Sender.Name = "office@school.example.test"
		run, _ = engine.Classify(context.Background(), email, testCategories())
		if len(run.Results) != 0 {
			t.Fatal("sender display name or suffix fooled rule")
		}
	}
}
func TestRulesRejectInvalidConfiguration(t *testing.T) {
	for _, data := range []string{`{"name":"x","version":"1","rules":[],"unknown":true}`, `{} {}`, strings.Repeat(" ", 65537)} {
		if _, err := ParseConfig([]byte(data)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid config accepted")
		}
	}
	for _, change := range []func(*RuleConfig){
		func(c *RuleConfig) { c.Rules[0].Confidence = 1.1 }, func(c *RuleConfig) { c.Rules[0].All[0].Field = "url_fetch" }, func(c *RuleConfig) { c.Rules[0].All[0].Reason = "" }, func(c *RuleConfig) { c.Rules[0].All = nil }, func(c *RuleConfig) { c.Rules[1].ID = c.Rules[0].ID },
	} {
		config := StarterConfig()
		change(&config)
		if _, err := NewRuleBasedClassifier(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid rule accepted")
		}
	}
}
func TestInputFingerprintIncludesMutableClassificationInputs(t *testing.T) {
	email := fixture("Subject", "Body")
	categories := testCategories()
	original := InputHash(email, categories)
	email.Labels = []domain.EmailLabel{{Name: "Newsletters", Kind: domain.LabelKindUser}}
	if InputHash(email, categories) == original {
		t.Fatal("labels omitted")
	}
	labelsHash := InputHash(email, categories)
	email.Labels[0].Name = "Other"
	if InputHash(email, categories) == labelsHash {
		t.Fatal("label names omitted")
	}
	email.Labels = nil
	categories[0].Description = "Changed meaning"
	if InputHash(email, categories) == original {
		t.Fatal("category definition omitted")
	}
	categories = testCategories()
	categories[0].Enabled = false
	if InputHash(email, categories) == original {
		t.Fatal("category enabled state omitted")
	}
	categories = testCategories()
	reversed := append([]domain.EmailCategory{}, categories...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if InputHash(email, reversed) != original {
		t.Fatal("category order affected fingerprint")
	}
	engine := classifier(t)
	run1, _ := engine.Classify(context.Background(), fixture("Your receipt", "Payment received. Order number: 1."), categories)
	run2, _ := engine.Classify(context.Background(), fixture("Your receipt", "Payment received. Order number: 1."), categories)
	if !reflect.DeepEqual(run1.Results[0].Reasons, run2.Results[0].Reasons) {
		t.Fatal("reasons are not deterministic")
	}
}
