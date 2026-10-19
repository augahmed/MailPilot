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

type aiFixtureService struct{ healthy }

func (aiFixtureService) ManageAISettings(context.Context, app.AISettingsRequest) app.AISettingsResponse {
	return app.AISettingsResponse{Providers: []app.AIProviderOption{}, Code: "unauthorized"}
}
func (aiFixtureService) SummarizeEmail(context.Context, app.SummarizeEmailRequest) app.SummarizeEmailResponse {
	return app.SummarizeEmailResponse{State: "idle", Code: "unauthorized"}
}
func TestAIContractsAndNarrowAllowlist(t *testing.T) {
	for _, test := range []struct {
		path     string
		method   string
		request  any
		response any
	}{{"ai-settings-v1.json", "ai.settings", new(app.AISettingsRequest), new(app.AISettingsResponse)}, {"email-summary-v1.json", "email.summarize", new(app.SummarizeEmailRequest), new(app.SummarizeEmailResponse)}} {
		raw, err := os.ReadFile("../../../contracts/" + test.path)
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Request  json.RawMessage
			Response json.RawMessage
		}
		if json.Unmarshal(raw, &fixture) != nil || strictDecode(fixture.Request, test.request) != nil || strictDecode(fixture.Response, test.response) != nil {
			t.Fatal("invalid shared contract")
		}
		for _, patch := range []string{`"prompt":"private-canary"`, `"instruction":"private-canary"`, `"includeThread":true`, `"emailIds":["a","b"]`, `"tools":["gmail"]`, `"operation":"generate"`, `"operation":"answer_with_context"`, `"operation":"summarize_thread"`, `"operation":"classify"`, `"operation":"chat"`, `"operation":"create"`} {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(fixture.Request, &fields)
			var extra map[string]json.RawMessage
			_ = json.Unmarshal([]byte("{"+patch+"}"), &extra)
			for k, v := range extra {
				fields[k] = v
			}
			params, _ := json.Marshal(fields)
			input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"ai","method":"` + test.method + `","params":` + string(params) + "}\n"
			var output bytes.Buffer
			if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return aiFixtureService{}, nil }); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "private-canary") {
				t.Fatal("generic or leaking AI request accepted")
			}
		}
	}
}
