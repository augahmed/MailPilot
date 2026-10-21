//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

var tuneClassification = flag.Bool("classification.tune", false, "evaluate tuning split to select a semantic policy")

type evalMessage struct{ ID, Split, Category, Subject, Body string }
type semanticEvalPoint struct {
	ID, Split, Expected, Scenario string
	Snapshot                      repository.SemanticSnapshot
	Scores                        []domain.CategoryScore
	RuleID                        domain.CategoryID
	RuleScore                     float64
	RuleConflict                  bool
}
type categoryMetric struct {
	TruePositive, FalsePositive, FalseNegative, Support int
	Precision, Recall                                   float64
}
type classificationMetrics struct {
	Total, Accepted, Correct, Abstained               int
	SuggestedLabels, CorrectLabels, MultipleSuggested int
	Coverage, AbstentionRate, Precision               float64
	Categories                                        map[string]categoryMetric
	Confusion                                         map[string]map[string]int
}

func evalChoices(p semanticEvalPoint, policy classification.SemanticPolicy, rules bool) ([]domain.CategoryID, string) {
	if rules && p.RuleID != "" && p.RuleScore >= policy.RuleThreshold {
		return []domain.CategoryID{p.RuleID}, "rules"
	}
	if rules && p.RuleConflict {
		return nil, "rule_conflict"
	}
	if len(p.Scores) == 0 {
		return nil, "insufficient"
	}
	scores := classification.SuggestedScores(p.Scores, policy.MinimumScore, policy.MinimumMargin)
	ids := []domain.CategoryID{}
	for _, s := range scores {
		ids = append(ids, s.CategoryID)
	}
	if len(ids) == 0 {
		return nil, "weak"
	}
	return ids, "semantic"
}
func evalMetrics(points []semanticEvalPoint, policy classification.SemanticPolicy, rules bool, names map[domain.CategoryID]string) classificationMetrics {
	m := classificationMetrics{Categories: map[string]categoryMetric{}, Confusion: map[string]map[string]int{}}
	for _, name := range names {
		m.Categories[name] = categoryMetric{}
	}
	for _, p := range points {
		m.Total++
		ids, _ := evalChoices(p, policy, rules)
		got := map[string]bool{}
		for _, id := range ids {
			got[names[id]] = true
		}
		if len(ids) == 0 {
			m.Abstained++
		} else {
			m.Accepted++
		}
		if len(ids) > 1 {
			m.MultipleSuggested++
		}
		m.SuggestedLabels += len(ids)
		if p.Expected != "" && got[p.Expected] {
			m.CorrectLabels++
			if len(ids) == 1 {
				m.Correct++
			}
		}
		expected := p.Expected
		if expected == "" {
			expected = "Unclassified"
		}
		if m.Confusion[expected] == nil {
			m.Confusion[expected] = map[string]int{}
		}
		if len(ids) == 0 {
			m.Confusion[expected]["Unclassified"]++
		}
		for name := range got {
			m.Confusion[expected][name]++
		}
		for name, c := range m.Categories {
			if p.Expected == name {
				c.Support++
				if got[name] {
					c.TruePositive++
				} else {
					c.FalseNegative++
				}
			} else if got[name] {
				c.FalsePositive++
			}
			m.Categories[name] = c
		}
	}
	for name, c := range m.Categories {
		if c.TruePositive+c.FalsePositive > 0 {
			c.Precision = float64(c.TruePositive) / float64(c.TruePositive+c.FalsePositive)
		}
		if c.Support > 0 {
			c.Recall = float64(c.TruePositive) / float64(c.Support)
		}
		m.Categories[name] = c
	}
	if m.Total > 0 {
		m.Coverage = float64(m.Accepted) / float64(m.Total)
		m.AbstentionRate = float64(m.Abstained) / float64(m.Total)
	}
	if m.SuggestedLabels > 0 {
		m.Precision = float64(m.CorrectLabels) / float64(m.SuggestedLabels)
	}
	return m
}
func TestNativeSemanticClassificationEvaluation(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../classification/testdata/semantic-eval-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Version int
		Cases   []evalMessage
	}
	if json.Unmarshal(raw, &data) != nil || data.Version != 1 {
		t.Fatal("invalid evaluation fixture")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	// Replay a deterministic forwarding wrapper over the existing split texts.
	// These are regression variants, not additional independent held-out samples.
	// Do not use these variants to retune the previously frozen thresholds.
	forwards := []evalMessage{}
	for _, c := range data.Cases {
		if c.Split == "confirmed" {
			continue
		}
		c.ID = "forwarded-" + c.ID
		c.Body = "---------- Forwarded message ---------\nFrom: Sample Sender <synthetic@example.test>\nSubject: " + c.Subject + "\n\n" + c.Body
		c.Subject = "Fwd: " + c.Subject
		forwards = append(forwards, c)
	}
	data.Cases = append(data.Cases, forwards...)
	seen := map[string]bool{}
	splitCount := map[string]int{}
	initial, db, _, _, _ := indexFixture(t, 0)
	initial.Close(ctx)
	for _, c := range data.Cases {
		if seen[c.ID] || c.ID == "" {
			t.Fatal("repeated example")
		}
		seen[c.ID] = true
		splitCount[c.Split]++
		e := domain.Email{ID: domain.EmailID(c.ID), AccountID: "a", ProviderID: domain.ProviderMessageID(c.ID), Subject: c.Subject, BodyText: c.Body, BodyState: "complete", Sender: domain.EmailAddress{Address: "synthetic@example.test"}, ReceivedAt: time.Unix(1700000000, 0)}
		if _, err = db.Emails().Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	meter := &nativeIndexMeter{Provider: provider}
	index := newTestIndex(t, db, meter)
	index.EnableCategoryVectors(db.CategoryVectors())
	wait := func() {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			p := indexCommand(index, "status")
			if p.Status != "running" {
				if p.Status != "complete" {
					t.Fatal(p)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("evaluation indexing timeout")
	}
	indexCommand(index, "start")
	wait()
	categories, _ := db.Categories().List(ctx, "a")
	names := map[domain.CategoryID]string{}
	ids := map[string]domain.CategoryID{}
	for _, c := range categories {
		names[c.ID] = c.Name
		ids[c.Name] = c.ID
	}
	rules, err := classification.SemanticRules()
	if err != nil {
		t.Fatal(err)
	}
	points := []semanticEvalPoint{}
	for scenario := 0; scenario < 2; scenario++ {
		if scenario == 1 {
			for _, c := range data.Cases {
				if c.Split != "confirmed" {
					continue
				}
				stored, _ := db.Emails().Get(ctx, "a", domain.EmailID(c.ID))
				if err = db.CategoryVectors().Confirm(ctx, "a", ids[c.Category], stored.Email.ID, stored.ContentHash); err != nil {
					t.Fatal(err)
				}
			}
			indexCommand(index, "start")
			wait()
		}
		for _, c := range data.Cases {
			if c.Split == "confirmed" {
				continue
			}
			snapshot, err := db.Semantic().Snapshot(ctx, "a", domain.EmailID(c.ID), index.profile)
			if err != nil {
				t.Fatal(err)
			}
			rule, err := rules.Classify(ctx, snapshot.Email.Email, snapshot.Categories)
			if err != nil {
				t.Fatal(err)
			}
			p := semanticEvalPoint{ID: c.ID, Split: c.Split, Expected: c.Category, Snapshot: snapshot, Scenario: []string{"definition_only", "confirmed_examples"}[scenario], RuleConflict: rule.Reason == "Specific rules match competing categories; no category chosen."}
			if strings.HasPrefix(c.ID, "forwarded-") {
				p.Scenario = "forwarded_" + p.Scenario
			}
			if len(rule.Results) == 1 {
				p.RuleID = rule.Results[0].CategoryID
				p.RuleScore = rule.Results[0].Confidence
			}
			points = append(points, p)
		}
	}
	policy := classification.DefaultSemanticPolicy()
	policy.Version = classification.SemanticVersion
	policy.DatasetHash = digest
	policy.ModelVersion = index.profile.Model.Version
	policy.Fingerprint = index.profile.Fingerprint
	policy.PreprocessingVersion = index.profile.PreprocessingVersion
	candidates := []classification.SemanticPolicy{}
	if *tuneClassification {
		bestCorrect := -1
		bestFalse := 1000000
		for _, minimumWords := range []int{0, 4, 8, 12} {
			for _, weights := range [][3]float64{{.25, .15, .05}, {.4, .25, .1}, {.6, .25, .05}, {.8, .1, 0}} {
				base := policy
				base.MinimumAuthoredWords = minimumWords
				base.SubjectWeight = weights[0]
				base.AuthoredWeight = 1
				base.ForwardedWeight = weights[1]
				base.QuotedWeight = weights[2]
				tune := []semanticEvalPoint{}
				for _, point := range points {
					if point.Split != "tune" || strings.HasPrefix(point.Scenario, "forwarded_") {
						continue
					}
					point.Scores, err = classification.Aggregate(ctx, point.Snapshot.Chunks, point.Snapshot.Prototypes, base)
					if err != nil {
						t.Fatal(err)
					}
					tune = append(tune, point)
				}
				for minScore := 20; minScore <= 70; minScore += 2 {
					for margin := 0; margin <= 20; margin += 2 {
						for _, ruleThreshold := range []float64{.6, .65, .7, .8, .9, 1.01} {
							p := base
							p.MinimumScore = float64(minScore) / 100
							p.MinimumMargin = float64(margin) / 100
							p.RuleThreshold = ruleThreshold
							combined := classificationMetrics{Categories: map[string]categoryMetric{}}
							for _, useRules := range []bool{false, true} {
								m := evalMetrics(tune, p, useRules, names)
								combined.Accepted += m.Accepted
								combined.Correct += m.Correct
								for name, c := range m.Categories {
									total := combined.Categories[name]
									total.TruePositive += c.TruePositive
									total.FalsePositive += c.FalsePositive
									combined.Categories[name] = total
								}
							}
							eligible := combined.Accepted > 0 && float64(combined.Correct)/float64(combined.Accepted) >= .9
							falseCount := combined.Accepted - combined.Correct
							if eligible && (combined.Correct > bestCorrect || combined.Correct == bestCorrect && falseCount < bestFalse) {
								bestCorrect = combined.Correct
								bestFalse = falseCount
								candidates = []classification.SemanticPolicy{p}
							}
						}
					}
				}
			}
		}
		if len(candidates) == 0 {
			for _, point := range points {
				if point.Split != "tune" || point.Scenario != "definition_only" {
					continue
				}
				scores, e := classification.Aggregate(ctx, point.Snapshot.Chunks, point.Snapshot.Prototypes, policy)
				if e != nil {
					t.Fatal(e)
				}
				ranked := []string{}
				for _, score := range scores[:min(3, len(scores))] {
					ranked = append(ranked, fmt.Sprintf("%s=%.3f", names[score.CategoryID], score.Score))
				}
				t.Log("TUNING_DIAGNOSTIC", point.ID, point.Expected, ranked, len(point.Snapshot.Chunks), point.Snapshot.Availability)
			}
			t.Fatal("tuning cannot meet the predeclared 90% precision gate")
		}
		policy = candidates[0]
	} else {
		frozen := classification.DefaultSemanticPolicy()
		if frozen.DatasetHash != digest || frozen.ModelVersion != policy.ModelVersion || frozen.Fingerprint != policy.Fingerprint || frozen.PreprocessingVersion != policy.PreprocessingVersion {
			t.Fatal("policy is not qualified for this dataset/model/preprocessing")
		}
		policy = frozen
	}
	report := map[string]any{"datasetHash": digest, "splitCounts": splitCount, "policy": policy, "metrics": map[string]classificationMetrics{}, "forwardedRegressionRecipe": "forwarded-wrapper-v1-derived-from-existing-splits"}
	metrics := report["metrics"].(map[string]classificationMetrics)
	latencies := []float64{}
	examples := []map[string]any{}
	for _, scenario := range []string{"definition_only", "confirmed_examples", "forwarded_definition_only", "forwarded_confirmed_examples"} {
		for _, split := range []string{"tune", "heldout"} {
			cohort := []semanticEvalPoint{}
			for _, p := range points {
				if p.Scenario != scenario || p.Split != split {
					continue
				}
				start := time.Now()
				p.Scores, err = classification.Aggregate(ctx, p.Snapshot.Chunks, p.Snapshot.Prototypes, policy)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(scenario, "forwarded_") && len(p.Scores) == 0 {
					t.Fatal("complete forwarded message lost semantic scores", p.ID, p.Snapshot.Availability)
				}
				latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
				cohort = append(cohort, p)
				got, signal := evalChoices(p, policy, true)
				predictions := []string{}
				for _, id := range got {
					predictions = append(predictions, names[id])
				}
				examples = append(examples, map[string]any{"id": p.ID, "split": p.Split, "scenario": p.Scenario, "expected": p.Expected, "predictions": predictions, "signal": signal})
			}
			for _, useRules := range []bool{false, true} {
				key := fmt.Sprintf("%s/%s/rules=%v", scenario, split, useRules)
				m := evalMetrics(cohort, policy, useRules, names)
				metrics[key] = m
				if split == "heldout" && m.Accepted > 0 && m.Precision < .9 {
					t.Logf("QUALITY_LIMITATION: held-out precision below 90%% target: %s precision=%.4f accepted=%d", key, m.Precision, m.Accepted)
				}
			}
		}
	}
	// Exercise the production engine and persistence with the frozen selection.
	engine, err := classification.NewSemanticClassifier(db.Semantic(), index.profile, rules, policy)
	if err != nil {
		t.Fatal(err)
	}
	meter.mu.Lock()
	before := meter.generated
	meter.mu.Unlock()
	for _, c := range data.Cases {
		if c.Split == "confirmed" {
			continue
		}
		stored, _ := db.Emails().Get(ctx, "a", domain.EmailID(c.ID))
		run, err := engine.Classify(ctx, stored.Email, categories)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Classifications().Replace(ctx, run); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Classifications().Get(ctx, "a", stored.Email.ID); err != nil {
			t.Fatal(err)
		}
	}
	meter.mu.Lock()
	extra := meter.generated - before
	meter.mu.Unlock()
	if extra != 0 {
		t.Fatal("classification reembedded mail", extra)
	}
	sort.Float64s(latencies)
	report["scoringP50Ms"] = latencies[len(latencies)/2]
	report["scoringP95Ms"] = latencies[min(len(latencies)-1, int(float64(len(latencies))*.95))]
	report["classificationEmbeddings"] = extra
	report["cases"] = examples
	out, _ := json.Marshal(report)
	t.Log("CLASSIFICATION_QUALIFICATION", string(out))
}
