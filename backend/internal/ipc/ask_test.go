package ipc

import (
	"bytes"
	"context"
	"mailpilot.local/core/internal/domain"
	"strings"
	"testing"
)

type askFixtureService struct{}

func (askFixtureService) Health(context.Context) domain.Health { return domain.Health{} }

func TestAskContractAndStrictIPC(t *testing.T) {
	for _, params := range []string{`{"operation":"status"}`, `{"operation":"status","sql":"private"}`, `{"operation":"generate","accountId":"a","previewId":"p","approved":false}`, `{"operation":"delete","accountId":"a"}`} {
		input := `{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}` + "\n" + `{"jsonrpc":"2.0","id":"2","method":"ask.manage","params":` + params + "}\n"
		var out bytes.Buffer
		if err := Serve(context.Background(), strings.NewReader(input), &out, func(context.Context) (HealthService, error) { return askFixtureService{}, nil }); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Method not found") || strings.Contains(out.String(), "private") || strings.Contains(out.String(), "unauthorized") {
			t.Fatal("retired Ask method reached application")
		}
	}
}
