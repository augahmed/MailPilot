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

type indexFixtureService struct{ healthy }

func (indexFixtureService) ManageIndex(context.Context, app.IndexRequest) app.IndexResponse {
	return app.IndexResponse{Status: "idle", Code: "unauthorized"}
}
func TestLocalIndexContractAndStrictBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/local-index-v1.json")
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
	var request app.IndexRequest
	var response app.IndexResponse
	if strictDecode(fixture.Request, &request) != nil || !request.Valid() || strictDecode(fixture.Response, &response) != nil || response.IndexedMessages != 20 {
		t.Fatal("invalid contract")
	}
	for _, params := range []string{`{"operation":"start","accountId":""}`, `{"operation":"download","accountId":"a"}`, `{"operation":"start","accountId":"a","modelPath":"private-canary"}`, `{"operation":"status","accountId":"","emailIds":["x"]}`, `{"operation":"start","accountId":"a","prompt":"private-canary"}`} {
		input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"index","method":"index.local","params":` + params + "}\n"
		var output bytes.Buffer
		if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return indexFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "private-canary") {
			t.Fatal("unsafe index request accepted")
		}
	}
}
