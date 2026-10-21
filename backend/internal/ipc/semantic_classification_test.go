package ipc

import (
	"encoding/json"
	"mailpilot.local/core/internal/app"
	"os"
	"testing"
)

func TestLocalClassificationContractAndOverrideBounds(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/classification-local-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Request, OverrideRequest, Response json.RawMessage }
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	for _, raw := range []json.RawMessage{fixture.Request, fixture.OverrideRequest} {
		var r app.ClassificationRequest
		if strictDecode(raw, &r) != nil || !r.Valid() {
			t.Fatal("invalid request")
		}
	}
	var response app.ClassificationPage
	if strictDecode(fixture.Response, &response) != nil || response.Rows[0].Semantic == nil {
		t.Fatal("invalid response")
	}
	var r app.ClassificationRequest
	strictDecode(fixture.OverrideRequest, &r)
	r.Revision = ""
	if r.Valid() {
		t.Fatal("unbound override accepted")
	}
	r.Operation = "run"
	r.Revision = ""
	if r.Valid() {
		t.Fatal("run accepted override controls")
	}
}
