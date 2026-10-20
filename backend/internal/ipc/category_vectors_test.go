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

type categoryVectorFixtureService struct{ healthy }

func (categoryVectorFixtureService) ManageCategoryVectors(context.Context, app.CategoryVectorRequest) app.CategoryVectorResponse {
	return app.CategoryVectorResponse{Status: "unavailable", Code: "unauthorized", DefinitionInputs: []string{}, Examples: []app.CategoryExampleDTO{}}
}
func TestCategoryVectorContractAndStrictConfirmation(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/category-vectors-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  json.RawMessage
		Response json.RawMessage
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	var request app.CategoryVectorRequest
	var response app.CategoryVectorResponse
	if strictDecode(fixture.Request, &request) != nil || !request.Valid() || strictDecode(fixture.Response, &response) != nil || len(response.Examples) != 1 {
		t.Fatal("invalid contract")
	}
	for _, params := range []string{`{"operation":"confirm_example","accountId":"a","categoryId":"c","emailId":"e","revision":"","approved":false}`, `{"operation":"inspect","accountId":"a","categoryId":"c","emailId":"","revision":"","approved":false,"provider":"private-canary"}`, `{"operation":"predict","accountId":"a","categoryId":"c"}`} {
		input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"vector","method":"categories.vectors","params":` + params + "}\n"
		var output bytes.Buffer
		if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return categoryVectorFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "private-canary") {
			t.Fatal("unsafe parameters accepted")
		}
	}
}
