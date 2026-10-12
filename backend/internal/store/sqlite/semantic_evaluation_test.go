package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/minilm"
)

func TestOptInMiniLMEvaluation(t *testing.T) {
	python, model := os.Getenv("MAILPILOT_TEST_EMBEDDING_PYTHON"), os.Getenv("MAILPILOT_TEST_EMBEDDING_MODEL_DIR")
	if python == "" || model == "" {
		t.Skip("real model evaluation requires explicitly installed model paths")
	}
	ctx := context.Background()
	p, err := minilm.New(ctx, python, model)
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	a := account(t, s, "synthetic-evaluation")
	embeddings, err := embedding.NewLocalClassificationService(p, s.Emails(), s.Vectors(), s.CategoryVectors())
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := classification.NewSemanticClassifier(embeddings, classification.DefaultSemanticConfig())
	if err != nil {
		t.Fatal(err)
	}
	rules, err := classification.NewRuleBasedClassifier(classification.StarterConfig())
	if err != nil {
		t.Fatal(err)
	}
	hybrid, err := classification.NewHybridClassifier(rules, semantic, classification.DefaultHybridConfig())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../classification/testdata/labeled_emails.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []struct {
		ID, Subject, Body string
		Category          *string
	}
	if err = json.Unmarshal(data, &examples); err != nil {
		t.Fatal(err)
	}
	categories, err := s.Categories().List(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	names := map[domain.CategoryID]string{}
	for _, c := range categories {
		names[c.ID] = c.Name
	}
	type counts struct{ TP, FP, FN int }
	byCategory := map[string]*counts{}
	for _, c := range categories {
		byCategory[c.Name] = &counts{}
	}
	correct, selected, truePositive, top1, positiveExamples := 0, 0, 0, 0, 0
	for _, example := range examples {
		e := domain.Email{AccountID: a, ProviderID: domain.ProviderMessageID(example.ID), Subject: example.Subject, BodyText: example.Body, BodyState: "complete", ReceivedAt: time.Unix(1, 0), Sender: domain.EmailAddress{Address: "synthetic@example.test"}}
		e.ID = saveEmail(t, s, e)
		run, err := hybrid.Classify(ctx, e, categories)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Classifications().Replace(ctx, run); err != nil {
			t.Fatal(err)
		}
		stored, err := s.Classifications().Get(ctx, a, e.ID)
		if err != nil || stored.Reason != run.Reason || len(stored.Candidates) != len(run.Candidates) {
			t.Fatal("evaluation persistence", err)
		}
		predicted := ""
		if len(run.Results) > 0 {
			predicted = names[run.Results[0].CategoryID]
			selected++
		}
		expected := ""
		if example.Category != nil {
			expected = *example.Category
			positiveExamples++
			if len(run.Candidates) > 0 && names[run.Candidates[0].CategoryID] == expected {
				top1++
			}
		}
		if predicted == expected {
			correct++
			if predicted != "" {
				truePositive++
				byCategory[predicted].TP++
			}
		} else {
			if predicted != "" {
				byCategory[predicted].FP++
			}
			if expected != "" {
				byCategory[expected].FN++
			}
		}
		t.Logf("%s: expected=%q selected=%q reason=%s", example.ID, expected, predicted, run.Reason)
	}
	precision := 0.0
	if selected > 0 {
		precision = float64(truePositive) / float64(selected)
	}
	t.Logf("MINILM EVALUATION total=%d correct=%d accuracy=%.4f selected=%d coverage=%.4f micro_precision=%.4f labeled_top1=%d/%d", len(examples), correct, float64(correct)/float64(len(examples)), selected, float64(selected)/float64(len(examples)), precision, top1, positiveExamples)
	for _, c := range categories {
		m := byCategory[c.Name]
		t.Logf("%s TP=%d FP=%d FN=%d", c.Name, m.TP, m.FP, m.FN)
	}
}
