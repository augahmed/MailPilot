package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
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
