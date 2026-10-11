package app

import (
	"context"
	"testing"
)

func TestCategoryRequestValidation(t *testing.T) {
	yes := true
	for _, p := range []CategoryRequest{{Operation: "classify"}, {Operation: "delete"}, {Operation: "save", AccountID: "a", Name: "n", Description: "d"}, {Operation: "list", ID: "foreign"}, {Operation: "list", Offset: -1}} {
		if p.Valid() {
			t.Fatal("accepted invalid request")
		}
	}
	for _, p := range []CategoryRequest{{Operation: "list"}, {Operation: "save", AccountID: "a", Name: "n", Description: "meaningful", Enabled: &yes}, {Operation: "delete", AccountID: "a", ID: "c"}} {
		if !p.Valid() {
			t.Fatal("rejected valid request")
		}
	}
	result := (&App{}).ManageCategories(context.Background(), CategoryRequest{Operation: "list"})
	if result.Code != "unauthorized" || len(result.Rows) != 0 {
		t.Fatal("unauthorized access")
	}
}
