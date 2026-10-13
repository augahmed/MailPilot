package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/domain"
	"os"
	"strings"
	"testing"
)

type askFixtureService struct{}

func (askFixtureService) Health(context.Context) domain.Health { return domain.Health{} }
func (askFixtureService) AskMail(context.Context, app.AskRequest) app.AskResponse {
	return (&app.App{}).AskMail(context.Background(), app.AskRequest{})
}
func TestAskContractAndStrictIPC(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/ask-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  json.RawMessage
		Response json.RawMessage
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("fixture")
	}
	var req app.AskRequest
	var resp app.AskResponse
	if strictDecode(fixture.Request, &req) != nil || !req.Valid() || strictDecode(fixture.Response, &resp) != nil || resp.State != "answered" {
		t.Fatal("invalid contract")
	}
	for _, params := range []string{`{"operation":"status"}`, `{"operation":"status","sql":"private"}`, `{"operation":"generate","accountId":"a","previewId":"p","approved":false}`, `{"operation":"delete","accountId":"a"}`} {
		input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"2","method":"ask.manage","params":` + params + "}\n"
		var out bytes.Buffer
		if err := Serve(context.Background(), strings.NewReader(input), &out, func(context.Context) (HealthService, error) { return askFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if params == `{"operation":"status"}` {
			if !strings.Contains(out.String(), "unauthorized") {
				t.Fatal("dispatch")
			}
		} else if !strings.Contains(out.String(), "Invalid parameters") || strings.Contains(out.String(), "private") {
			t.Fatal("unvalidated request")
		}
	}
}
