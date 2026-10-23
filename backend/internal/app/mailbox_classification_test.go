package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func TestMailboxClassificationContract(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/mailbox-classification-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var page MailboxPage
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Classification.CategoryID != "finance" {
		t.Fatal("missing classification summary")
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if json.Unmarshal(data, &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("contract fields lost")
	}
}

func TestMailboxClassificationUsesFreshLocalAssessmentsWithoutInference(t *testing.T) {
	ctx := context.Background()
	index, db, embedder, _, review := semanticAppFixture(t, 2)
	a := &App{Classification: review}
	categories, err := db.Categories().List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	names := map[domain.CategoryID]string{}
	for _, category := range categories {
		names[category.ID] = category.Name
	}
	id := domain.EmailID("mail-000000")
	before, err := a.mailClassification(ctx, "a", id, names)
	if err != nil || before.State != "unclassified" {
		t.Fatal(before, err)
	}
	indexCommand(index, "start")
	if p := waitIndex(t, index); p.Status != "complete" {
		t.Fatal(p)
	}
	calls := embedder.count()
	current, err := a.mailClassification(ctx, "a", id, names)
	if err != nil || current.State != "suggested" || current.CategoryID == "" || current.CategoryName == "" {
		t.Fatal(current, err)
	}
	stored, err := db.Emails().Get(ctx, "a", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Semantic().SetPreference(ctx, "a", id, "assigned", categories[0].ID, stored.ContentHash); err != nil {
		t.Fatal(err)
	}
	// A changed preference hides the old suggestion until separately reassessed.
	stale, err := a.mailClassification(ctx, "a", id, names)
	if err != nil || stale.State != "stale" || stale.CategoryID != "" {
		t.Fatal(stale, err)
	}
	if embedder.count() != calls {
		t.Fatal("mailbox summary performed inference")
	}
}
