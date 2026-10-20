//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"mailpilot.local/core/internal/embedding"
	mailtext "mailpilot.local/core/internal/mail"
)

func TestNativeCategoryPrototypesSynthetic(t *testing.T) {
	ctx := context.Background()
	initial, db, _, emails, _ := indexFixture(t, 1)
	initial.Close(ctx)
	provider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	meter := &nativeIndexMeter{Provider: provider}
	s := newTestIndex(t, db, meter)
	s.EnableCategoryVectors(db.CategoryVectors())
	start := time.Now()
	indexCommand(s, "start")
	p := waitIndex(t, s)
	elapsed := time.Since(start)
	if p.Status != "complete" || p.ReadyCategories != 6 || p.IndexedMessages != 1 {
		t.Fatal("native prototypes failed", p)
	}
	categories, _ := db.Categories().List(ctx, "a")
	for _, c := range categories {
		source, err := db.CategoryVectors().Source(ctx, "a", c.ID, s.profile)
		if err != nil || source.Cached == nil || !validIndexVector(source.Cached.Vector, 384) || len(source.Cached.DefinitionInputs) == 0 {
			t.Fatal("invalid native representation", err)
		}
	}
	stored, _ := db.Emails().Get(ctx, "a", emails[0].ID)
	if r := categoryRequest(s, "confirm_example", categories[0].ID, emails[0].ID, mailtext.ReaderRevision(stored.ContentHash)); r.Code != "" {
		t.Fatal(r)
	}
	meter.mu.Lock()
	before := meter.generated
	meter.mu.Unlock()
	indexCommand(s, "start")
	p = waitIndex(t, s)
	meter.mu.Lock()
	extra := meter.generated - before
	meter.mu.Unlock()
	if p.ReadyCategories != 6 || extra != 0 {
		t.Fatal("native example work did not reuse shared vectors", p, extra)
	}
	foreign, err := db.CategoryVectors().Progress(ctx, "b", s.profile)
	if err != nil || foreign.Ready != 0 || foreign.Pending != 6 {
		t.Fatal("native account isolation", foreign, err)
	}
	report, _ := json.Marshal(map[string]any{"categories": p.ReadyCategories, "dimension": 384, "initialEmbeddings": before, "exampleUpdateEmbeddings": extra, "initialMilliseconds": float64(elapsed.Microseconds()) / 1000, "network": "denied", "classification": false})
	t.Log("CATEGORY_QUALIFICATION", string(report))
}
