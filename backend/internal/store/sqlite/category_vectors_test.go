package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func categoryVectorFixture(t *testing.T) (*Store, repository.IndexRun, domain.CategoryID, repository.ChunkSnapshot, []repository.EmailChunk) {
	t.Helper()
	s, run, input, chunks := indexStoreFixture(t)
	run.Profile.CategoryRecipe = repository.CategoryRecipe
	var err error
	run, err = s.Indexing().Begin(context.Background(), run.AccountID, run.Profile, false)
	if err != nil {
		t.Fatal(err)
	}
	cats, err := s.Categories().List(context.Background(), run.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, cats[0].ID, input, chunks
}
func categoryPrototype(source repository.CategoryVectorSource) repository.CategoryPrototype {
	status := "ready"
	for _, e := range source.Examples {
		if e.Status != "included" {
			status = "partial"
		}
	}
	return repository.CategoryPrototype{SourceHash: source.SourceHash, DefinitionHash: source.DefinitionHash, Status: status, DefinitionInputs: []string{"Subject: " + source.Category.Name, source.Category.Description}, DefinitionVector: []float32{1, 0}, Vector: []float32{1, 0}}
}
func TestCategoryVectorSnapshotInvalidationAndNoImplicitExamples(t *testing.T) {
	ctx := context.Background()
	s, run, id, input, chunks := categoryVectorFixture(t)
	r := s.CategoryVectors()
	a := run.AccountID
	source, err := r.Source(ctx, a, id, run.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Save(ctx, run, source, categoryPrototype(source)); err != nil {
		t.Fatal(err)
	}
	cached, _ := r.Source(ctx, a, id, run.Profile)
	if cached.Cached == nil || cached.Cached.Status != "ready" || len(cached.Cached.Vector) != 2 {
		t.Fatal("missing cache", cached)
	}
	// Neither deterministic results nor manual assignments constitute confirmation.
	if err = s.Categories().Assign(ctx, a, input.EmailID, id); err != nil {
		t.Fatal(err)
	}
	if err = s.Classifications().Replace(ctx, assessment(t, s, a, input.EmailID, id)); err != nil {
		t.Fatal(err)
	}
	predicted, _ := r.Source(ctx, a, id, run.Profile)
	if len(predicted.Examples) != 0 || predicted.SourceHash != cached.SourceHash {
		t.Fatal("prediction or assignment used as training")
	}
	if err = r.Confirm(ctx, a, id, input.EmailID, input.SourceRevision); err != nil {
		t.Fatal(err)
	}
	pending, _ := r.Source(ctx, a, id, run.Profile)
	if pending.Cached.Status != "stale" || len(pending.Cached.Vector) != 0 || len(pending.Cached.DefinitionVector) != 2 || len(pending.Examples) != 1 || pending.Examples[0].Status != "index_pending" {
		t.Fatal("pending example/cache incorrect", pending)
	}
	if err = r.Save(ctx, run, source, categoryPrototype(source)); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("old example set published", err)
	}
	if err = r.Save(ctx, run, pending, categoryPrototype(pending)); err != nil {
		t.Fatal(err)
	}
	partial, _ := r.Source(ctx, a, id, run.Profile)
	if partial.Cached.Status != "partial" {
		t.Fatal("incomplete example should be partial")
	}
	state, _ := s.Chunks().State(ctx, a, input.EmailID)
	readyChunks(t, s, state, chunks)
	included, _ := r.Source(ctx, a, id, run.Profile)
	if included.Cached.Status != "stale" || included.Examples[0].Status != "included" || len(included.Examples[0].Chunks) != 2 {
		t.Fatal("complete chunks did not invalidate partial prototype", included)
	}
	if err = r.Save(ctx, run, partial, categoryPrototype(partial)); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale generation published", err)
	}
	if err = r.Save(ctx, run, included, categoryPrototype(included)); err != nil {
		t.Fatal(err)
	}
	// A -> B -> A edits are distinct source revisions, while unchanged definition
	// text can still reuse its cached definition vector after a confirmation edit.
	original := included.Category
	c := original
	c.Description = "A temporary changed definition."
	s.Categories().Save(ctx, a, c)
	s.Categories().Save(ctx, a, original)
	reverted, _ := r.Source(ctx, a, id, run.Profile)
	if reverted.SourceHash == included.SourceHash || reverted.DefinitionHash != included.DefinitionHash {
		t.Fatal("missing ABA fence")
	}
	if err = r.Save(ctx, run, included, categoryPrototype(included)); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("pre-edit worker restored prototype", err)
	}
	c.Description = "An edited natural language category definition."
	s.Categories().Save(ctx, a, c)
	edited, _ := r.Source(ctx, a, id, run.Profile)
	if edited.DefinitionHash == reverted.DefinitionHash || len(edited.Cached.DefinitionVector) != 0 {
		t.Fatal("incompatible definition cache reused")
	}
	if err = r.Save(ctx, run, edited, categoryPrototype(edited)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Categories().Get(ctx, a, id)
	if got.Description != c.Description {
		t.Fatal("derived write overwrote manual edit")
	}
}
func TestCategoryVectorAccountModelAndRunFences(t *testing.T) {
	ctx := context.Background()
	s, run, id, input, _ := categoryVectorFixture(t)
	r := s.CategoryVectors()
	a := run.AccountID
	b := account(t, s, "foreign-category")
	foreignCats, _ := s.Categories().List(ctx, b)
	if _, err := r.Source(ctx, b, id, run.Profile); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	if err := r.Confirm(ctx, b, id, input.EmailID, input.SourceRevision); err == nil {
		t.Fatal("foreign category confirmed")
	}
	if err := r.Confirm(ctx, a, foreignCats[0].ID, input.EmailID, input.SourceRevision); err == nil {
		t.Fatal("foreign association confirmed")
	}
	other, otherInput := chunkFixture(t, s, b, "foreign-email")
	if err := r.Confirm(ctx, a, id, other.ID, otherInput.SourceRevision); err == nil {
		t.Fatal("foreign email confirmed")
	}
	if _, err := s.db.Exec(`INSERT INTO category_examples VALUES(?,?,?,?,?,?)`, b, id, other.ID, otherInput.SourceRevision, "foreign", 0); err == nil {
		t.Fatal("composite FK missing")
	}
	source, _ := r.Source(ctx, a, id, run.Profile)
	if err := r.Save(ctx, run, source, categoryPrototype(source)); err != nil {
		t.Fatal(err)
	}
	changed := run.Profile
	changed.Model.Version = "recipe-v2"
	replacement, err := s.Indexing().Begin(ctx, a, changed, false)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := r.Source(ctx, a, id, changed)
	if fresh.Cached != nil || fresh.SourceHash == source.SourceHash {
		t.Fatal("model versions mixed")
	}
	if err = r.Save(ctx, run, source, categoryPrototype(source)); err == nil {
		t.Fatal("old model worker published")
	}
	if err = r.Save(ctx, replacement, fresh, categoryPrototype(fresh)); err != nil {
		t.Fatal(err)
	}
	badFingerprint := changed
	badFingerprint.Fingerprint = hashText("different model artifacts")
	if _, err = s.Indexing().Begin(ctx, a, badFingerprint, false); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("same model version silently changed artifacts", err)
	}
	old, _ := r.Source(ctx, a, id, run.Profile)
	if old.Cached == nil || !reflect.DeepEqual(old.Cached.Vector, []float32{1, 0}) {
		t.Fatal("old generation lost")
	}
	if err = s.Indexing().Transition(ctx, replacement, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if err = r.Save(ctx, replacement, fresh, categoryPrototype(fresh)); err == nil {
		t.Fatal("paused worker published")
	}
	// A preprocessing change cannot read the previous prototype.
	prep := changed
	prep.PreprocessingVersion = "clean-v2"
	pre, _ := r.Source(ctx, a, id, prep)
	if pre.Cached != nil {
		t.Fatal("preprocessing versions mixed")
	}
}
func TestCategoryExampleRevisionDeletionAndLimits(t *testing.T) {
	ctx := context.Background()
	s, run, id, input, chunks := categoryVectorFixture(t)
	r := s.CategoryVectors()
	a := run.AccountID
	state, _ := s.Chunks().State(ctx, a, input.EmailID)
	readyChunks(t, s, state, chunks)
	if err := r.Confirm(ctx, a, id, input.EmailID, input.SourceRevision); err != nil {
		t.Fatal(err)
	}
	source, _ := r.Source(ctx, a, id, run.Profile)
	if err := r.Save(ctx, run, source, categoryPrototype(source)); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Emails().Get(ctx, a, input.EmailID)
	stored.Email.BodyText = "Different confirmed evidence after a local source refresh."
	saveEmail(t, s, stored.Email)
	changed, _ := r.Source(ctx, a, id, run.Profile)
	if changed.Examples[0].Status != "source_changed" || len(changed.Examples[0].Chunks) != 0 || len(changed.Cached.Vector) != 0 {
		t.Fatal("source change reused evidence")
	}
	if err := r.Save(ctx, run, source, categoryPrototype(source)); err == nil {
		t.Fatal("stale source published")
	}
	if err := r.Confirm(ctx, a, id, input.EmailID, input.SourceRevision); err == nil {
		t.Fatal("old source confirmation accepted")
	}
	if err := r.Save(ctx, run, changed, categoryPrototype(changed)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, a, input.EmailID); err != nil {
		t.Fatal(err)
	}
	purged, _ := r.Source(ctx, a, id, run.Profile)
	if len(purged.Examples) != 0 || len(purged.Cached.Vector) != 0 || purged.Cached.Status != "stale" {
		t.Fatal("purged mail retained in prototype")
	}
	if err := r.Save(ctx, run, changed, categoryPrototype(changed)); err == nil {
		t.Fatal("purged evidence restored")
	}
	for i := 0; i < repository.MaxCategoryExamples; i++ {
		e, in := chunkFixture(t, s, a, string(rune('A'+i)))
		if err := r.Confirm(ctx, a, id, e.ID, in.SourceRevision); err != nil {
			t.Fatal(err)
		}
	}
	extra, in := chunkFixture(t, s, a, "extra")
	if err := r.Confirm(ctx, a, id, extra.ID, in.SourceRevision); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("unbounded examples", err)
	}
	c, _ := s.Categories().Get(ctx, a, id)
	c.Enabled = false
	s.Categories().Save(ctx, a, c)
	if err := r.Confirm(ctx, a, id, extra.ID, in.SourceRevision); err == nil {
		t.Fatal("disabled category accepted confirmation")
	}
}
func TestCategoryDefaultsUpgradePreservesManualDefinitionsAndReferences(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 17)
	a := account(t, old, "untouched")
	b := account(t, old, "edited")
	c := account(t, old, "referenced")
	var applicationsB, applicationsC domain.CategoryID
	old.db.QueryRow(`SELECT id FROM categories WHERE account_id=? AND name='Job Applications'`, b).Scan(&applicationsB)
	old.db.QueryRow(`SELECT id FROM categories WHERE account_id=? AND name='Job Applications'`, c).Scan(&applicationsC)
	custom, _ := old.Categories().Get(ctx, b, applicationsB)
	custom.Description = "My manually edited application category."
	old.Categories().Save(ctx, b, custom)
	e := sampleEmail(c, "reference")
	email := saveEmail(t, old, e)
	if err := old.Categories().Assign(ctx, c, email, applicationsC); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, account := range []domain.AccountID{a, b, c} {
		rows, err := s.Categories().List(ctx, account)
		if err != nil {
			t.Fatal(err)
		}
		if account == a {
			names := []string{}
			for _, v := range rows {
				names = append(names, v.Name)
			}
			if !reflect.DeepEqual(names, []string{"Finance", "Jobs", "Newsletters", "Personal", "Receipts", "School"}) {
				t.Fatal("initial categories", names)
			}
		} else if len(rows) != 7 {
			t.Fatal("edited/referenced defaults collapsed", account, rows)
		}
	}
	got, _ := s.Categories().Get(ctx, b, applicationsB)
	if got.Description != custom.Description {
		t.Fatal("manual definition reset")
	}
	stored, _ := s.Emails().Get(ctx, c, email)
	if len(stored.Email.Categories) != 1 || stored.Email.Categories[0].ID != applicationsC {
		t.Fatal("legacy assignment rewritten")
	}
	var violations int
	s.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations)
	if violations != 0 {
		t.Fatal("foreign key violations")
	}
}
