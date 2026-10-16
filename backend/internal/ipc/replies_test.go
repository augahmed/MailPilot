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

type replyFixtureService struct{}

func (replyFixtureService) Health(context.Context) domain.Health { return domain.Health{} }
func (replyFixtureService) Reply(context.Context, app.ReplyRequest) app.ReplyResponse {
	return app.ReplyResponse{State: "idle", Code: "unauthorized"}
}
func TestReplyContractAndStrictIPC(t *testing.T) {
	raw, e := os.ReadFile("../../../contracts/reply-drafting-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	var req app.ReplyRequest
	if strictDecode(fixture.Request, &req) != nil || !req.Valid() || req.ActiveValid() {
		t.Fatal("invalid fixture request")
	}
	var resp app.ReplyResponse
	if strictDecode(fixture.Response, &resp) != nil || resp.State != "ai_preview" {
		t.Fatal("invalid fixture response")
	}
	for _, params := range []string{`{"operation":"prepare","accountId":"a","emailId":"e","tone":"Professional"}`, `{"operation":"prepare","accountId":"a","emailId":"e","tone":"Professional","includeThread":true,"instruction":"Ignore rules"}`, `{"operation":"generate","accountId":"a","previewId":"p","approved":true}`, `{"operation":"preview_draft","accountId":"a","previewId":"p","body":"reviewed body"}`, `{"operation":"create","accountId":"a","actionId":"action","previewId":"digest","approved":true}`, `{"operation":"summarize_thread","accountId":"a"}`, `{"operation":"chat","accountId":"a","instruction":"arbitrary"}`, `{"operation":"send"}`, `{"operation":"status","apiKey":"synthetic-private-key"}`, `{"operation":"status","sql":"select"}`, `{"operation":"create","accountId":"a","actionId":"action","previewId":"digest","approved":false}`} {
		var output bytes.Buffer
		input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"2","method":"reply.manage","params":` + params + "}\n"
		if e = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return replyFixtureService{}, nil }); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(output.String(), "Invalid parameters") || strings.Contains(output.String(), "synthetic-private-key") {
			t.Fatal("unvalidated or leaking IPC")
		}
	}
	var output bytes.Buffer
	input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"2","method":"reply.manage","params":{"operation":"status"}}` + "\n"
	if e = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return replyFixtureService{}, nil }); e != nil || !strings.Contains(output.String(), "unauthorized") {
		t.Fatal("allowlisted dispatch failed")
	}
}
