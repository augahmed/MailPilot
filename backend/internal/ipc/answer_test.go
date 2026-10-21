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

type answerFixtureService struct{ healthy }

func (answerFixtureService) AnswerEmail(context.Context, app.AnswerRequest) app.AnswerResponse {
	return app.AnswerResponse{State: "idle", Code: "unauthorized"}
}
func TestAnswerStrictContractAndNoModelTools(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/email-answer-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("fixture")
	}
	for _, key := range []string{"request", "approve"} {
		var request app.AnswerRequest
		if strictDecode(fixture[key], &request) != nil || !request.Valid() {
			t.Fatal("invalid request", key)
		}
	}
	for _, key := range []string{"preview", "response"} {
		var response app.AnswerResponse
		if strictDecode(fixture[key], &response) != nil || len(response.Evidence) != 1 {
			t.Fatal("invalid response", key)
		}
	}
	for _, params := range []string{`{"operation":"retrieve","accountId":"a","question":"When?","tools":["gmail"]}`, `{"operation":"retrieve","accountId":"a","question":"When?","provider":"private-canary"}`, `{"operation":"approve","accountId":"a","approved":true}`, `{"operation":"status","question":"private-canary"}`, `{"operation":"retrieve","accountId":"a","question":"When?","sourceText":"private-canary"}`, `{"operation":"retrieve","accountId":"a","question":"When?","filter":{"sql":"private-canary"}}`} {
		input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"q","method":"email.answer","params":` + params + "}\n"
		var output bytes.Buffer
		if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return answerFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "private-canary") {
			t.Fatal("capability/input accepted")
		}
	}
}
