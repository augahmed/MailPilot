package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

func categoryRequest(s *LocalIndex, op string, id domain.CategoryID, email domain.EmailID, revision string) CategoryVectorResponse {
	return s.ManageCategoryVectors(context.Background(), CategoryVectorRequest{Operation: op, AccountID: "a", CategoryID: string(id), EmailID: string(email), Revision: revision, Approved: op == "confirm_example"})
}
func TestCategoryPrototypesDefinitionCacheExamplesAndEdits(t *testing.T) {
	ctx := context.Background()
	s, db, f, emails, _ := indexFixture(t, 1)
	s.EnableCategoryVectors(db.CategoryVectors())
	cats, _ := db.Categories().List(ctx, "a")
	if len(cats) != 6 {
		t.Fatal("six initial categories required")
	}
	if p := indexCommand(s, "status"); p.PendingCategories != 6 || f.count() != 0 {
		t.Fatal("inference was not opt-in", p)
	}
	before := categoryRequest(s, "inspect", cats[0].ID, "", "")
	if before.Status != "pending" || len(before.DefinitionInputs) == 0 || f.count() != 0 {
		t.Fatal(before)
	}
	for _, text := range before.DefinitionInputs {
		if !strings.Contains(text, cats[0].Name) {
			t.Fatal("missing name context", text)
		}
	}
	indexCommand(s, "start")
	p := waitIndex(t, s)
	if p.Status != "complete" || p.ReadyCategories != 6 || p.PendingCategories != 0 {
		t.Fatalf("not all categories represented: %+v", p)
	}
	cached, err := db.CategoryVectors().Source(ctx, "a", cats[0].ID, s.profile)
	if err != nil || cached.Cached == nil || !validIndexVector(cached.Cached.Vector, s.profile.Model.Dimension) {
		t.Fatal("invalid prototype", err)
	}
	calls := f.count()
	indexCommand(s, "start")
	waitIndex(t, s)
	if f.count() != calls {
		t.Fatal("cache replay invoked inference")
	}
	// A manual assignment is not an explicitly confirmed training example.
	if err = db.Categories().Assign(ctx, "a", emails[0].ID, cats[0].ID); err != nil {
		t.Fatal(err)
	}
	inspect := categoryRequest(s, "inspect", cats[0].ID, "", "")
	if len(inspect.Examples) != 0 {
		t.Fatal("assignment became a training example")
	}
	stored, _ := db.Emails().Get(ctx, "a", emails[0].ID)
	confirmed := categoryRequest(s, "confirm_example", cats[0].ID, emails[0].ID, mailtext.ReaderRevision(stored.ContentHash))
	if confirmed.Code != "" || confirmed.Status != "pending" || len(confirmed.Examples) != 1 || confirmed.Examples[0].Status != "included" {
		t.Fatal(confirmed)
	}
	indexCommand(s, "start")
	p = waitIndex(t, s)
	if p.ReadyCategories != 6 || f.count() != calls {
		t.Fatal("example reembedded shared mail or unchanged definition", p, f.count(), calls)
	}
	withExample := categoryRequest(s, "inspect", cats[0].ID, "", "")
	if withExample.SourceHash == inspect.SourceHash || withExample.Examples[0].Chunks[0].Text == "" {
		t.Fatal("missing source explanation", withExample)
	}
	// Removing/reconfirming the same source is a new explicit confirmation fence.
	categoryRequest(s, "remove_example", cats[0].ID, emails[0].ID, "")
	categoryRequest(s, "confirm_example", cats[0].ID, emails[0].ID, mailtext.ReaderRevision(stored.ContentHash))
	indexCommand(s, "start")
	waitIndex(t, s)
	if f.count() != calls {
		t.Fatal("confirmation-only change reran inference")
	}
	re := categoryRequest(s, "inspect", cats[0].ID, "", "")
	if re.SourceHash == withExample.SourceHash {
		t.Fatal("ABA confirmation reused source identity")
	}
	// Preserve the user's edits and regenerate only the changed definition.
	c := cats[0]
	c.Name = "My bank records"
	c.Description = "Account statements and banking notices, excluding shopping receipts."
	if _, err = db.Categories().Save(ctx, "a", c); err != nil {
		t.Fatal(err)
	}
	if p = indexCommand(s, "status"); p.PendingCategories != 1 || p.Status != "pending" {
		t.Fatal("edit not invalidated", p)
	}
	indexCommand(s, "resume")
	p = waitIndex(t, s)
	if p.ReadyCategories != 6 || f.count() <= calls {
		t.Fatal("edited definition was not embedded", p)
	}
	got, _ := db.Categories().Get(ctx, "a", c.ID)
	if got.Name != c.Name || got.Description != c.Description {
		t.Fatal("prototype generation overwrote edit")
	}
	calls = f.count()
	// Original source revision remains explicit; a sync edit must not silently train on changed text.
	emails[0].BodyText = "A changed project update with different evidence."
	if _, err = db.Emails().Upsert(ctx, emails[0]); err != nil {
		t.Fatal(err)
	}
	if r := categoryRequest(s, "confirm_example", c.ID, emails[0].ID, mailtext.ReaderRevision(stored.ContentHash)); r.Code != "stale" {
		t.Fatal("stale reader confirmation accepted", r)
	}
	indexCommand(s, "start")
	p = waitIndex(t, s)
	r := categoryRequest(s, "inspect", c.ID, "", "")
	if p.Status != "partial" || p.PartialCategories != 1 || r.Status != "partial" || r.Examples[0].Status != "source_changed" || len(r.Examples[0].Chunks) != 0 {
		t.Fatal("changed example not excluded", p, r)
	}
	if f.count()-calls != 1 {
		t.Fatal("unchanged definition reembedded alongside updated mail", f.count()-calls)
	}
	current, _ := db.Emails().Get(ctx, "a", emails[0].ID)
	r = categoryRequest(s, "confirm_example", c.ID, emails[0].ID, mailtext.ReaderRevision(current.ContentHash))
	if r.Code != "" {
		t.Fatal(r)
	}
	calls = f.count()
	indexCommand(s, "start")
	p = waitIndex(t, s)
	if p.Status != "complete" || f.count() != calls {
		t.Fatal("reconfirmation did not reuse vectors", p)
	}
	c.Enabled = false
	db.Categories().Save(ctx, "a", c)
	if p = indexCommand(s, "status"); p.EligibleCategories != 5 || p.ReadyCategories != 5 {
		t.Fatal("disabled counted", p)
	}
	if r = categoryRequest(s, "inspect", c.ID, "", ""); r.Status != "disabled" {
		t.Fatal(r)
	}
	// No assessment/suggestion is created. The only assignment is the one above.
	assessed, err := db.Classifications().Get(ctx, "a", emails[0].ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("prototype work classified mail", assessed, err)
	}
}
func TestCategoryPrototypesModelChangeIsolationAndOptIn(t *testing.T) {
	ctx := context.Background()
	s, db, f, _, _ := indexFixture(t, 0)
	s.EnableCategoryVectors(db.CategoryVectors())
	cats, _ := db.Categories().List(ctx, "a")
	foreign, _ := db.Categories().List(ctx, "b")
	if r := categoryRequest(s, "inspect", foreign[0].ID, "", ""); r.Code != "not_found" {
		t.Fatal("foreign category disclosed", r)
	}
	bad := CategoryVectorRequest{Operation: "inspect", AccountID: "b", CategoryID: string(foreign[0].ID)}
	if r := s.ManageCategoryVectors(ctx, bad); r.Code != "unauthorized" {
		t.Fatal(r)
	}
	indexCommand(s, "start")
	waitIndex(t, s)
	if p, _ := db.CategoryVectors().Progress(ctx, "b", s.profile); p.Ready != 0 || p.Pending != 6 {
		t.Fatal("cross account indexing", p)
	}
	s.Close(ctx)
	f2 := &indexFake{descriptor: f.descriptor}
	f2.descriptor.Version = "synthetic-category-model-v2"
	next := newTestIndex(t, db, f2)
	next.EnableCategoryVectors(db.CategoryVectors())
	if p := indexCommand(next, "status"); p.PendingCategories != 6 || f2.count() != 0 {
		t.Fatal("model change mixed prototypes or auto-inferred", p)
	}
	r := categoryRequest(next, "inspect", cats[0].ID, "", "")
	if r.Status != "pending" || r.SourceHash != "" {
		t.Fatal(r)
	}
	indexCommand(next, "resume")
	p := waitIndex(t, next)
	if p.ReadyCategories != 6 || f2.count() == 0 {
		t.Fatal("new model did not rebuild", p)
	}
	old, _ := db.CategoryVectors().Source(ctx, "a", cats[0].ID, s.profile)
	fresh, _ := db.CategoryVectors().Source(ctx, "a", cats[0].ID, next.profile)
	if old.Cached == nil || fresh.Cached == nil || old.SourceHash == fresh.SourceHash || old.Profile.Model == fresh.Profile.Model {
		t.Fatal("model generations collapsed")
	}
}
func TestCategoryInferenceEditAndCancellationFences(t *testing.T) {
	ctx := context.Background()
	s, db, f, _, _ := indexFixture(t, 0)
	s.EnableCategoryVectors(db.CategoryVectors())
	cats, _ := db.Categories().List(ctx, "a")
	// Use one enabled category so the in-flight definition is deterministic.
	for _, c := range cats[1:] {
		c.Enabled = false
		db.Categories().Save(ctx, "a", c)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	f.invoke = func(ctx context.Context, text []string) ([][]float32, error) {
		close(entered)
		select {
		case <-release:
			return fakeIndexVectors(text, f.descriptor.Dimensions), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	indexCommand(s, "start")
	<-entered
	c := cats[0]
	c.Description = "Changed while the category model was running."
	if _, err := db.Categories().Save(ctx, "a", c); err != nil {
		t.Fatal("inference held SQLite transaction", err)
	}
	if p := indexCommand(s, "pause"); p.Status != "paused" {
		t.Fatal(p)
	}
	close(release)
	waitIndex(t, s)
	source, err := db.CategoryVectors().Source(ctx, "a", c.ID, s.profile)
	if err != nil || source.Cached != nil && len(source.Cached.Vector) > 0 {
		t.Fatal("stale worker published", err)
	}
	f.mu.Lock()
	f.invoke = nil
	f.mu.Unlock()
	indexCommand(s, "resume")
	if p := waitIndex(t, s); p.ReadyCategories != 1 {
		t.Fatal(p)
	}
}
func TestCategoryUnitMeanAndWire(t *testing.T) {
	mean, err := unitMean([][]float32{{1, 0}, {0, 1}}, 2)
	if err != nil || mean[0] < 0.707 || mean[0] > 0.708 || mean[0] != mean[1] {
		t.Fatal(mean, err)
	}
	if _, err = unitMean([][]float32{{1, 0}, {-1, 0}}, 2); !errors.Is(err, embedding.ErrVector) {
		t.Fatal("zero prototype accepted", err)
	}
	data, err := os.ReadFile("../../../contracts/category-vectors-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  CategoryVectorRequest
		Response CategoryVectorResponse
	}
	if json.Unmarshal(data, &fixture) != nil || !fixture.Request.Valid() {
		t.Fatal("invalid fixture")
	}
	fixture.Request.Approved = false
	if fixture.Request.Valid() {
		t.Fatal("implicit confirmation accepted")
	}
}

func TestCategoryPrototypeWeightsDefinitionAndExampleCentroids(t *testing.T) {
	ctx := context.Background()
	s, db, f, emails, _ := indexFixture(t, 1)
	s.EnableCategoryVectors(db.CategoryVectors())
	f.invoke = func(_ context.Context, texts []string) ([][]float32, error) {
		out := fakeIndexVectors(texts, f.descriptor.Dimensions)
		for i, text := range texts {
			if strings.Contains(text, "Project update") {
				out[i][0] = 0
				out[i][1] = 1
			}
		}
		return out, nil
	}
	cats, _ := db.Categories().List(ctx, "a")
	indexCommand(s, "start")
	waitIndex(t, s)
	stored, _ := db.Emails().Get(ctx, "a", emails[0].ID)
	if r := categoryRequest(s, "confirm_example", cats[0].ID, emails[0].ID, mailtext.ReaderRevision(stored.ContentHash)); r.Code != "" {
		t.Fatal(r)
	}
	calls := f.count()
	indexCommand(s, "start")
	waitIndex(t, s)
	source, err := db.CategoryVectors().Source(ctx, "a", cats[0].ID, s.profile)
	if err != nil {
		t.Fatal(err)
	}
	v := source.Cached.Vector
	if v[0] < 0.707 || v[0] > 0.708 || v[0] != v[1] || f.count() != calls {
		t.Fatal("definition/example weighting or reuse incorrect", v[:2])
	}
}

func TestCategoryUpgradeAddsPendingWorkWithoutInvalidatingMail(t *testing.T) {
	ctx := context.Background()
	old, db, f, _, _ := indexFixture(t, 1)
	indexCommand(old, "start")
	waitIndex(t, old)
	old.Close(ctx)
	next := newTestIndex(t, db, f)
	next.EnableCategoryVectors(db.CategoryVectors())
	p := indexCommand(next, "status")
	if p.Status != "pending" || p.Code != "pending_changes" || p.IndexedMessages != 1 || p.PendingMessages != 0 || p.PendingCategories != 6 {
		t.Fatal("category upgrade invalidated email model", p)
	}
	calls := f.count()
	indexCommand(next, "start")
	p = waitIndex(t, next)
	if p.Status != "complete" || p.ReadyCategories != 6 || f.count()-calls != 12 {
		t.Fatal("category upgrade reembedded mail", p, f.count()-calls)
	}
}
