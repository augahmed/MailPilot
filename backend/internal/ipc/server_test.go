package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/domain"
	mailsync "mailpilot.local/core/internal/sync"
)

type healthy struct{}

func (healthy) Health(context.Context) domain.Health {
	return domain.Health{Backend: "connected", Database: "connected"}
}

func TestHandshakeAndAllowlist(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":"1","method":"health"}
{"jsonrpc":"2.0","id":"2","method":"initialize","params":{"protocolVersion":99}}
{"jsonrpc":"2.0","id":"3","method":"initialize","params":{"protocolVersion":1}}
{"jsonrpc":"2.0","id":"4","method":"health"}
{"jsonrpc":"2.0","id":"5","method":"exec"}
{"jsonrpc":"2.0","id":"6","method":"shutdown"}
`
	var output bytes.Buffer
	calls := 0
	err := Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { calls++; return healthy{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("factory called %d times", calls)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("unexpected responses: %s", output.String())
	}
	for i, want := range []int{-32001, -32602, 0, 0, -32601, 0} {
		var reply response
		if err := json.Unmarshal([]byte(lines[i]), &reply); err != nil {
			t.Fatal(err)
		}
		if want == 0 && reply.Error != nil {
			t.Fatalf("unexpected error: %+v", reply.Error)
		}
		if want != 0 && (reply.Error == nil || reply.Error.Code != want) {
			t.Fatalf("wrong error: %+v", reply.Error)
		}
	}
}

func TestMalformedAndOversizedFrames(t *testing.T) {
	for _, input := range []string{"{broken}\n", `{"jsonrpc":"2.0","id":"x","method":"health","unexpected":true}` + "\n"} {
		var output bytes.Buffer
		if err := Serve(context.Background(), strings.NewReader(input), &output, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Invalid request") {
			t.Fatal("accepted malformed frame")
		}
	}
	if err := Serve(context.Background(), strings.NewReader(strings.Repeat("x", MaxFrameBytes+1)), &bytes.Buffer{}, nil); err == nil {
		t.Fatal("accepted oversized frame")
	}
}

type accountsFake struct{ healthy }

func (accountsFake) AuthStatus() auth.Status     { return auth.Status{State: "disconnected"} }
func (accountsFake) AuthCheck() auth.Status      { return auth.Status{State: "checking"} }
func (accountsFake) AuthCancel() auth.Status     { return auth.Status{State: "disconnected"} }
func (accountsFake) AuthDisconnect() auth.Status { return auth.Status{State: "disconnecting"} }
func (accountsFake) AuthBegin() auth.BeginResult {
	return auth.BeginResult{Status: auth.Status{State: "connecting"}, AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth?client_id=synthetic"}
}
func TestAuthenticationCommandsAreNarrowAndRequireHandshake(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":"1","method":"auth.begin","params":{}}
{"jsonrpc":"2.0","id":"2","method":"initialize","params":{"protocolVersion":1}}
{"jsonrpc":"2.0","id":"3","method":"auth.begin","params":{"url":"https://evil.test"}}
{"jsonrpc":"2.0","id":"4","method":"auth.status","params":{}}
{"jsonrpc":"2.0","id":"5","method":"auth.begin","params":{}}
{"jsonrpc":"2.0","id":"6","method":"auth.check","params":{}}
{"jsonrpc":"2.0","id":"7","method":"auth.disconnect","params":{}}
{"jsonrpc":"2.0","id":"8","method":"auth.tokens","params":{}}
`
	var output bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return accountsFake{}, nil }); err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var result response
		if json.Unmarshal([]byte(line), &result) != nil {
			t.Fatal("invalid result")
		}
		want := []int{-32001, 0, -32602, 0, 0, 0, 0, -32601}[i]
		if want == 0 && result.Error != nil || want != 0 && (result.Error == nil || result.Error.Code != want) {
			t.Fatalf("unexpected result for request %d", i)
		}
	}
}

type mailFake struct{ healthy }

func (mailFake) SyncStart() mailsync.Progress  { return mailsync.Progress{State: "running", Limit: 500} }
func (mailFake) SyncStatus() mailsync.Progress { return mailsync.Progress{State: "idle", Limit: 500} }
func (mailFake) Mailbox(context.Context, int) app.MailboxPage {
	return app.MailboxPage{Rows: []app.MailboxRow{}}
}
func TestMailboxCommandsRejectAccountSelectionAndInvalidPages(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}
{"jsonrpc":"2.0","id":"2","method":"sync.start","params":{"accountId":"other"}}
{"jsonrpc":"2.0","id":"3","method":"sync.status","params":{}}
{"jsonrpc":"2.0","id":"4","method":"mail.list","params":{"offset":-1}}
{"jsonrpc":"2.0","id":"5","method":"mail.list","params":{"offset":0,"accountId":"other"}}
{"jsonrpc":"2.0","id":"6","method":"mail.list","params":{"offset":0}}
{"jsonrpc":"2.0","id":"7","method":"sync.start","params":{}}
`
	var output bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return mailFake{}, nil }); err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var r response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		want := []int{0, -32602, 0, -32602, -32602, 0, 0}[i]
		if want == 0 && r.Error != nil || want != 0 && (r.Error == nil || r.Error.Code != want) {
			t.Fatalf("unexpected reply %d: %+v", i, r.Error)
		}
	}
}

type categoryService struct {
	healthy
	calls int
}

func (s *categoryService) ManageCategories(_ context.Context, p app.CategoryRequest) app.CategoryPage {
	s.calls++
	return app.CategoryPage{AccountID: p.AccountID, Rows: []app.CategoryDTO{}}
}
func TestCategoriesContractAndValidation(t *testing.T) {
	body, err := os.ReadFile("../../../contracts/categories-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  app.CategoryRequest `json:"request"`
		Response app.CategoryPage    `json:"response"`
	}
	if err = strictDecode(body, &fixture); err != nil || !fixture.Request.Valid() {
		t.Fatal("incompatible fixture", err)
	}
	params, _ := json.Marshal(fixture.Request)
	input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"save","method":"categories.manage","params":` + string(params) + "}\n" +
		`{"jsonrpc":"2.0","id":"bad","method":"categories.manage","params":{"operation":"save","name":"x","enabled":true}}` + "\n" +
		`{"jsonrpc":"2.0","id":"unknown","method":"categories.manage","params":{"operation":"list","sql":"bad"}}` + "\n"
	service := &categoryService{}
	var output bytes.Buffer
	if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return service, nil }); err != nil {
		t.Fatal(err)
	}
	if service.calls != 1 || strings.Count(output.String(), "Invalid parameters") != 2 {
		t.Fatal("invalid category dispatch")
	}
}

type classificationService struct {
	healthy
	calls int
}

func (s *classificationService) ReviewClassifications(_ context.Context, p app.ClassificationRequest) app.ClassificationPage {
	s.calls++
	return app.ClassificationPage{AccountID: p.AccountID, Rows: []app.ClassificationRow{}}
}
func TestClassificationContractAndStrictRequests(t *testing.T) {
	body, err := os.ReadFile("../../../contracts/classification-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  app.ClassificationRequest `json:"request"`
		Response app.ClassificationPage    `json:"response"`
	}
	if err = strictDecode(body, &fixture); err != nil || !fixture.Request.Valid() {
		t.Fatal("classification fixture mismatch", err)
	}
	if len(fixture.Response.Rows) != 2 || fixture.Response.Rows[0].Predictions[0].ClassifierName != "RuleBasedClassifier" || len(fixture.Response.Rows[0].Predictions[0].Reasons) != 3 {
		t.Fatal("incomplete classification fixture")
	}
	params, _ := json.Marshal(fixture.Request)
	input := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":"run","method":"classification.review","params":` + string(params) + "}\n" +
		`{"jsonrpc":"2.0","id":"bad","method":"classification.review","params":{"operation":"run","accountId":"","offset":0}}` + "\n" +
		`{"jsonrpc":"2.0","id":"unknown","method":"classification.review","params":{"operation":"list","accountId":"a","offset":0,"body":"untrusted"}}` + "\n" +
		`{"jsonrpc":"2.0","id":"negative","method":"classification.review","params":{"operation":"list","accountId":"a","offset":-1}}` + "\n"
	service := &classificationService{}
	var output bytes.Buffer
	if err = Serve(context.Background(), strings.NewReader(input), &output, func(context.Context) (HealthService, error) { return service, nil }); err != nil {
		t.Fatal(err)
	}
	if service.calls != 1 || strings.Count(output.String(), "Invalid parameters") != 3 {
		t.Fatal("invalid classification dispatch")
	}
}
