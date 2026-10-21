package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func semanticStoreFixture(t *testing.T) (*Store, repository.IndexRun, *classification.SemanticClassifier, domain.EmailID) {
	t.Helper()
	ctx := context.Background()
	s, run, id, input, _ := categoryVectorFixture(t)
	input.Chunks[0].SectionType = "subject"
	input.Chunks[1].SectionType = "authored"
	state, chunks := replaceChunks(t, s, input)
	batch := repository.EmbeddingBatch{State: state}
	for _, c := range chunks {
		batch.Embeddings = append(batch.Embeddings, repository.ChunkEmbedding{ChunkID: c.ID, Status: "ready", Vector: []float32{1, 0}})
	}
	if _, err := s.Chunks().SaveEmbeddings(ctx, batch); err != nil {
		t.Fatal(err)
	}
	cats, _ := s.Categories().List(ctx, run.AccountID)
	for _, c := range cats {
		source, err := s.CategoryVectors().Source(ctx, run.AccountID, c.ID, run.Profile)
		if err != nil {
			t.Fatal(err)
		}
		prototype := categoryPrototype(source)
		if c.ID != id {
			prototype.Vector = []float32{0, 1}
		}
		if err = s.CategoryVectors().Save(ctx, run, source, prototype); err != nil {
			t.Fatal(err)
		}
	}
	p := classification.DefaultSemanticPolicy()
	p.ModelVersion = run.Profile.Model.Version
	p.Fingerprint = run.Profile.Fingerprint
	p.PreprocessingVersion = run.Profile.PreprocessingVersion
	engine, err := classification.NewSemanticClassifier(s.Semantic(), run.Profile, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.Semantic().Prepare(ctx, run, engine.Version())
	if err != nil {
		t.Fatal(err)
	}
	return s, run, engine, input.EmailID
}
func assessSemantic(t *testing.T, s *Store, run repository.IndexRun, engine *classification.SemanticClassifier, id domain.EmailID) domain.ClassificationRun {
	t.Helper()
	snapshot, err := s.Semantic().Snapshot(context.Background(), run.AccountID, id, run.Profile)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Assess(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestSemanticPersistenceFreshnessAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	s, run, engine, id := semanticStoreFixture(t)
	result := assessSemantic(t, s, run, engine, id)
	if len(result.Results) != 1 || len(result.Semantic.Scores) != 6 || len(result.Semantic.Scores[0].Evidence) != 2 {
		t.Fatal("missing aggregated evidence", result)
	}
	if err := s.Semantic().SaveIndexed(ctx, run, result); err != nil {
		t.Fatal(err)
	}
	got, err := s.Classifications().Get(ctx, run.AccountID, id)
	if err != nil || got.Semantic.Profile.ModelVersion != run.Profile.Model.Version || got.Semantic.Scores[0].Score != result.Results[0].Confidence {
		t.Fatal(got, err)
	}
	other := account(t, s, "other-semantic")
	if _, err = s.Semantic().Snapshot(ctx, other, id, run.Profile); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign snapshot", err)
	}
	if _, err = s.Classifications().Get(ctx, other, id); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign assessment", err)
	}
	if err = s.Semantic().SetPreference(ctx, other, id, "assigned", result.Results[0].CategoryID, result.Semantic.SourceRevision); err == nil {
		t.Fatal("foreign override")
	}
	c, _ := s.Categories().Get(ctx, run.AccountID, result.Results[0].CategoryID)
	original := c
	c.Description = "An edited category definition."
	s.Categories().Save(ctx, run.AccountID, c)
	s.Categories().Save(ctx, run.AccountID, original)
	if _, err = s.Classifications().Get(ctx, run.AccountID, id); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("category edit did not revoke assessment", err)
	}
	if err = s.Semantic().SaveIndexed(ctx, run, result); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("ABA category edit allowed stale worker", err)
	}
}

func TestSemanticPreviousAlgorithmRequiresReassessment(t *testing.T) {
	ctx := context.Background()
	s, run, engine, id := semanticStoreFixture(t)
	result := assessSemantic(t, s, run, engine, id)
	if err := s.Semantic().SaveIndexed(ctx, run, result); err != nil {
		t.Fatal(err)
	}
	// Simulate a saved assessment from the previous installed classifier.
	result.Semantic.Version = "semantic-v1-section-means-dedup"
	data, err := json.Marshal(result.Semantic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE classification_runs SET semantic_json=? WHERE account_id=? AND email_id=?", string(data), run.AccountID, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Classifications().Get(ctx, run.AccountID, id); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("previous algorithm should be stale, not a storage error", err)
	}
}
func TestSemanticExampleVectorOverrideAndRunInvalidation(t *testing.T) {
	for _, change := range []string{"example", "vector", "source", "override", "pause", "restart", "model", "purge"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, run, engine, id := semanticStoreFixture(t)
			result := assessSemantic(t, s, run, engine, id)
			if err := s.Semantic().SaveIndexed(ctx, run, result); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "example":
				if err := s.CategoryVectors().Confirm(ctx, run.AccountID, result.Results[0].CategoryID, id, result.Semantic.SourceRevision); err != nil {
					t.Fatal(err)
				}
			case "vector":
				if _, err := s.db.Exec(`UPDATE chunk_embeddings SET vector_data=vector_data WHERE account_id=?`, run.AccountID); err != nil {
					t.Fatal(err)
				}
			case "source":
				e, _ := s.Emails().Get(ctx, run.AccountID, id)
				e.Email.BodyText = "New content."
				saveEmail(t, s, e.Email)
			case "override":
				if err := s.Semantic().SetPreference(ctx, run.AccountID, id, "unclassified", "", result.Semantic.SourceRevision); err != nil {
					t.Fatal(err)
				}
			case "pause":
				s.Indexing().Transition(ctx, run, "paused", "")
			case "restart":
				s.Indexing().Recover(ctx)
			case "model":
				p := run.Profile
				p.Model.Version = "another-model"
				s.Indexing().Begin(ctx, run.AccountID, p, false)
			case "purge":
				if _, err := s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, run.AccountID, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Semantic().SaveIndexed(ctx, run, result); err == nil {
				t.Fatal("revoked worker published")
			}
			if change == "example" || change == "vector" || change == "source" || change == "override" {
				if _, err := s.Classifications().Get(ctx, run.AccountID, id); !errors.Is(err, repository.ErrConflict) && !errors.Is(err, repository.ErrNotFound) {
					t.Fatal("stale prediction remained visible", err)
				}
			}
		})
	}
}
func TestSemanticLabelChangesAreScopedAndOverridesPreserveAssignments(t *testing.T) {
	ctx := context.Background()
	s, run, engine, id := semanticStoreFixture(t)
	original := assessSemantic(t, s, run, engine, id)
	other := sampleEmail(run.AccountID, "second")
	other.ID = saveEmail(t, s, other)
	// A sibling import does not alter this email's evidence; categories/index edits do.
	snapshot, _ := s.Semantic().Snapshot(ctx, run.AccountID, id, run.Profile)
	original, _ = engine.Assess(ctx, snapshot)
	if err := s.Classifications().Replace(ctx, original); err != nil {
		t.Fatal(err)
	}
	other.Labels = []domain.EmailLabel{{AccountID: run.AccountID, ProviderID: "keep", Name: "Keep", Kind: domain.LabelKindUser}}
	saveEmail(t, s, other)
	if _, err := s.Classifications().Get(ctx, run.AccountID, id); err != nil {
		t.Fatal("another email label revoked this assessment", err)
	}
	cats, _ := s.Categories().List(ctx, run.AccountID)
	for _, c := range cats[:2] {
		if err := s.Categories().Assign(ctx, run.AccountID, id, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	result := assessSemantic(t, s, run, engine, id)
	if result.Semantic.State != "assigned" || len(result.Results) != 0 {
		t.Fatal("multiple assignments collapsed")
	}
	if err := s.Semantic().SetPreference(ctx, run.AccountID, id, "assigned", cats[2].ID, result.Semantic.SourceRevision); err != nil {
		t.Fatal(err)
	}
	selected := assessSemantic(t, s, run, engine, id)
	if selected.Results[0].CategoryID != cats[2].ID || selected.Results[0].ScoreKind != "user_assignment" {
		t.Fatal("primary override ignored")
	}
	stored, _ := s.Emails().Get(ctx, run.AccountID, id)
	if len(stored.Email.Categories) != 2 {
		t.Fatal("legacy assignments erased")
	}
	if err := s.Semantic().SetPreference(ctx, run.AccountID, id, "unclassified", "", selected.Semantic.SourceRevision); err != nil {
		t.Fatal(err)
	}
	abstained := assessSemantic(t, s, run, engine, id)
	if len(abstained.Results) != 0 || abstained.UsedClassifier != "user" {
		t.Fatal("explicit abstention ignored")
	}
}

func TestSemanticTombstoneClearsPreferenceAndAssessment(t *testing.T) {
	ctx := context.Background()
	s, run, engine, id := semanticStoreFixture(t)
	result := assessSemantic(t, s, run, engine, id)
	if err := s.Semantic().SetPreference(ctx, run.AccountID, id, "assigned", result.Results[0].CategoryID, result.Semantic.SourceRevision); err != nil {
		t.Fatal(err)
	}
	assigned := assessSemantic(t, s, run, engine, id)
	if err := s.Classifications().Replace(ctx, assigned); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE emails SET remote_deleted=1 WHERE account_id=? AND id=?`, run.AccountID, id); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "email_category_preferences") != 0 || count(t, s, "classification_runs") != 0 {
		t.Fatal("tombstone retained active preference/assessment")
	}
}
func TestSemanticMigrationPreservesLegacyRunsAndJournalBytes(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 18)
	a := account(t, old, "semantic-upgrade")
	plan := draftPlan(t, old, a)
	importHistoricalDraft(t, old, plan, "uncertain")
	cats, _ := old.Categories().List(ctx, a)
	stored, _ := old.Emails().Get(ctx, a, plan.EmailID)
	hash := classification.InputHash(stored.Email, cats)
	if _, err := old.db.Exec(`INSERT INTO classification_runs(account_id,email_id,classifier_name,classifier_version,input_hash,timestamp,used_classifier,reason,candidates_json) VALUES(?,?,?,?,?,1,'none','Legacy abstention.','[]')`, a, plan.EmailID, "LegacyClassifier", "legacy-v1", hash); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := old.db.QueryRow(`SELECT plan_json FROM reply_draft_actions WHERE account_id=? AND id=?`, a, plan.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Classifications().Get(ctx, a, plan.EmailID)
	if err != nil || got.Semantic != nil || got.InputHash != hash || got.ClassifierVersion != "legacy-v1" {
		t.Fatal("legacy assessment reinterpreted", got, err)
	}
	var after string
	if err = s.db.QueryRow(`SELECT plan_json FROM reply_draft_actions WHERE account_id=? AND id=?`, a, plan.ID).Scan(&after); err != nil || before != after {
		t.Fatal("immutable journal bytes changed", err)
	}
	if count(t, s, "email_category_preferences") != 0 || count(t, s, "classification_email_state") == 0 {
		t.Fatal("migration invented assignments or omitted source fences")
	}
}
