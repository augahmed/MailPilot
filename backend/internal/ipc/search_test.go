package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"mailpilot.local/core/internal/app"
	"os"
	"strings"
	"testing"
)

type searchFixtureService struct{ healthy }

func (searchFixtureService) SearchLocal(context.Context, app.SearchRequest) app.SearchResponse {
	return app.SearchResponse{Status: "idle", Code: "unauthorized"}
}
func TestLocalSearchContractAndStrictBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/vector-search-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  json.RawMessage
		Response json.RawMessage
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var request app.SearchRequest
	var response app.SearchResponse
	if strictDecode(fixture.Request, &request) != nil || !request.Valid() || strictDecode(fixture.Response, &response) != nil || len(response.Emails) != 1 {
		t.Fatal("invalid contract")
	}
	for _, mode := range []string{"keyword", "semantic", "hybrid"} {
		request.Mode = mode
		if !request.Valid() {
			t.Fatal("mode rejected", mode)
		}
	}
	request.Mode = "remote"
	if request.Valid() {
		t.Fatal("unknown mode accepted")
	}
	raw, err = os.ReadFile("../../../contracts/hybrid-search-v1.json")
	if err != nil || json.Unmarshal(raw, &fixture) != nil || strictDecode(fixture.Request, &request) != nil || !request.Valid() || strictDecode(fixture.Response, &response) != nil || response.Mode != "keyword" || response.Warning != "semantic_unavailable" {
		t.Fatal("invalid hybrid contract", err)
	}
	for _, params := range []string{`{"operation":"start","accountId":"a","jobId":"","query":"find","limit":21,"filter":{"sender":"","after":0,"before":0,"categoryId":""}}`, `{"operation":"start","accountId":"a","modelPath":"private-canary"}`, `{"operation":"status","accountId":"","filter":{"sql":"private-canary"}}`, `{"operation":"start","accountId":"a","provider":"private-canary"}`} {
		input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"index","method":"search.local","params":` + params + "}\n"
		var output bytes.Buffer
		if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return searchFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "private-canary") {
			t.Fatal("unsafe index request accepted")
		}
	}
}
