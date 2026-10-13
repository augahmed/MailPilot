package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/minilm"
)

type evaluationExample struct {
	ID, Subject, Body string
	Category          *string
	Split             string
	Case              string
	BodyState         *string `json:"body_state"`
}
type evaluationCounts struct {
	Total, Correct, Selected, TP, Positive, Negative, FalsePositive int
	Categories                                                      map[string]*categoryCounts
	Bins                                                            map[string]*categoryCounts
}
type categoryCounts struct{ TP, FP, FN int }

func (m *evaluationCounts) add(expected, predicted, kind string, score float64) {
	m.Total++
	if expected != "" {
		m.Positive++
	} else {
		m.Negative++
	}
	if expected == predicted {
		m.Correct++
	}
	if predicted != "" {
		m.Selected++
		if expected == predicted {
			m.TP++
		} else if expected == "" {
			m.FalsePositive++
		}
	}
	if m.Categories == nil {
		m.Categories = map[string]*categoryCounts{}
		m.Bins = map[string]*categoryCounts{}
	}
	for _, name := range []string{expected, predicted} {
		if name != "" && m.Categories[name] == nil {
			m.Categories[name] = &categoryCounts{}
		}
	}
	if predicted != "" {
		if expected == predicted {
			m.Categories[predicted].TP++
		} else {
			m.Categories[predicted].FP++
		}
	}
	if expected != "" && predicted != expected {
		m.Categories[expected].FN++
	}
	if predicted != "" {
		band := "<0.45"
		if score >= .60 {
			band = ">=0.60"
		} else if score >= .45 {
			band = "0.45–0.60"
		}
		key := kind + "/" + band
		if m.Bins[key] == nil {
			m.Bins[key] = &categoryCounts{}
		}
		if predicted == expected {
			m.Bins[key].TP++
		} else {
			m.Bins[key].FP++
		}
	}
}
func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
func (m *evaluationCounts) report(t *testing.T, label string) {
	t.Helper()
	t.Logf("METRICS %s total=%d selected=%d correct=%d assigned_correct=%d coverage=%.4f precision=%.4f accuracy=%.4f recall=%.4f negative_false_positives=%d/%d", label, m.Total, m.Selected, m.Correct, m.TP, ratio(m.Selected, m.Total), ratio(m.TP, m.Selected), ratio(m.Correct, m.Total), ratio(m.TP, m.Positive), m.FalsePositive, m.Negative)
	names := []string{}
	for name := range m.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := m.Categories[name]
		t.Logf("CATEGORY %s %s TP=%d FP=%d FN=%d precision=%.4f recall=%.4f", label, name, c.TP, c.FP, c.FN, ratio(c.TP, c.TP+c.FP), ratio(c.TP, c.TP+c.FN))
	}
	names = nil
	for name := range m.Bins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := m.Bins[name]
		t.Logf("SCORE_BIN %s %s correct=%d selected=%d precision=%.4f", label, name, c.TP, c.TP+c.FP, ratio(c.TP, c.TP+c.FP))
	}
}
func evaluationCorpus(t *testing.T) []evaluationExample {
	t.Helper()
	var all []evaluationExample
	for _, file := range []string{"labeled_emails.json", "expanded_emails.json"} {
		data, err := os.ReadFile("../../classification/testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var examples []evaluationExample
		if err = json.Unmarshal(data, &examples); err != nil {
			t.Fatal(err)
		}
		for i := range examples {
			if file == "labeled_emails.json" {
				examples[i].Split = "legacy"
				examples[i].ID = "legacy/" + examples[i].ID
			}
		}
		all = append(all, examples...)
	}
	return all
}
func TestEvaluationCorpusIntegrity(t *testing.T) {
	seen := map[string]bool{}
	texts := map[string]bool{}
	store := testStore(t)
	categories, err := store.Categories().List(context.Background(), account(t, store, "corpus-validation"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, category := range categories {
		allowed[category.Name] = true
	}
	positives, negatives := 0, 0
	for _, e := range evaluationCorpus(t) {
		if e.ID == "" || seen[e.ID] {
			t.Fatal("missing/duplicate ID")
		}
		seen[e.ID] = true
		text := e.Subject + "\x00" + e.Body
		// An empty-message control is intentionally repeated in the legacy and
		// expanded corpora; all nonempty messages must be distinct.
		if texts[text] && text != "\x00" {
			t.Fatal("duplicate nonempty message across corpus splits", e.ID)
		}
		texts[text] = true
		if e.Category != nil && !allowed[*e.Category] {
			t.Fatal("unknown expected category", e.ID)
		}
		if e.BodyState != nil && *e.BodyState != "" && *e.BodyState != "unavailable" && *e.BodyState != "complete" {
			t.Fatal("invalid body state", e.ID)
		}
		if e.Split != "legacy" && e.Split != "development" && e.Split != "validation" {
			t.Fatal("missing split")
		}
		if e.Category == nil {
			negatives++
		} else {
			positives++
		}
	}
	if len(seen) < 100 || positives < 60 || negatives < 35 {
		t.Fatal("insufficient corpus diversity", len(seen), positives, negatives)
	}
}
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
	// The old shipped hybrid never accepted a starter rule (max .65 < .85).
	// Its decisions were exactly semantic-only with these frozen thresholds.
	baselineConfig := classification.DefaultSemanticConfig()
	baselineConfig.MinimumSimilarity = .45
	baselineConfig.AmbiguityMargin = .05
	baselineConfig.TopCandidates = 3
	baseline, err := classification.NewSemanticClassifier(embeddings, baselineConfig)
	if err != nil {
		t.Fatal(err)
	}
	rulesOnly, err := classification.NewHybridClassifier(rules, nil, classification.DefaultHybridConfig())
	if err != nil {
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
	metrics := map[string]*evaluationCounts{}
	for _, example := range evaluationCorpus(t) {
		// Allows policy development without examining the reserved validation outcomes.
		if os.Getenv("MAILPILOT_EVALUATION_DEVELOPMENT_ONLY") == "1" && example.Split == "validation" {
			continue
		}
		state := "complete"
		if example.BodyState != nil {
			state = *example.BodyState
		}
		e := domain.Email{AccountID: a, ProviderID: domain.ProviderMessageID(example.ID), Subject: example.Subject, BodyText: example.Body, BodyState: state, ReceivedAt: time.Unix(1, 0), Sender: domain.EmailAddress{Address: "synthetic@example.test"}}
		e.ID = saveEmail(t, s, e)
		for _, engine := range []struct {
			name       string
			classifier classification.ClassificationEngine
		}{{"before", baseline}, {"after", hybrid}, {"rules-only", rulesOnly}} {
			run, err := engine.classifier.Classify(ctx, e, categories)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Classifications().Replace(ctx, run); err != nil {
				t.Fatal("persist", example.ID, engine.name, err)
			}
			stored, err := s.Classifications().Get(ctx, a, e.ID)
			if err != nil || stored.Reason != run.Reason || stored.ClassifierVersion != run.ClassifierVersion || len(stored.Candidates) != len(run.Candidates) {
				t.Fatal("evaluation persistence", err)
			}
			predicted, expected, kind := "", "", ""
			score := 0.0
			if len(run.Results) > 0 {
				predicted = names[run.Results[0].CategoryID]
				kind = run.Results[0].ScoreKind
				score = run.Results[0].Confidence
			}
			if example.Category != nil {
				expected = *example.Category
			}
			for _, split := range []string{example.Split, "all"} {
				key := engine.name + "/" + split
				if metrics[key] == nil {
					metrics[key] = &evaluationCounts{}
				}
				metrics[key].add(expected, predicted, kind, score)
			}
			type namedCandidate struct {
				Category string
				Cosine   float64
			}
			candidates := []namedCandidate{}
			for _, candidate := range run.Candidates {
				candidates = append(candidates, namedCandidate{names[candidate.CategoryID], candidate.Similarity})
			}
			t.Logf("DECISION %s %s expected=%q predicted=%q reason=%s candidates=%v", engine.name, example.ID, expected, predicted, run.Reason, candidates)
		}
	}
	legacy := metrics["before/legacy"]
	if legacy.Total != 18 || legacy.Selected != 8 || legacy.TP != 7 || legacy.Correct != 10 {
		t.Fatal("frozen baseline drifted", legacy)
	}
	keys := []string{}
	for k := range metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		metrics[k].report(t, k)
	}
}

func TestEvaluationMetricsIncludeAbstentionsAndWrongAssignments(t *testing.T) {
	m := &evaluationCounts{}
	for _, pair := range [][2]string{{"A", "A"}, {"A", ""}, {"A", "B"}, {"", "B"}, {"", ""}} {
		m.add(pair[0], pair[1], "cosine_similarity", .5)
	}
	if m.Total != 5 || m.Correct != 2 || m.Selected != 3 || m.TP != 1 || m.Positive != 3 || m.Negative != 2 || m.FalsePositive != 1 {
		t.Fatal("metric denominators omitted abstention or false assignment", m)
	}
	if *m.Categories["A"] != (categoryCounts{TP: 1, FN: 2}) || *m.Categories["B"] != (categoryCounts{FP: 2}) {
		t.Fatal("per-category errors miscounted", m.Categories)
	}
}
