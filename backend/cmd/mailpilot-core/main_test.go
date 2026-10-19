package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise actual composition, not just an IPC fake: stale model setup must not
// prevent startup or enable a worker, and retired calls cannot reach services.
func TestContractedRuntimeEntrypoints(t *testing.T) {
	if os.Getenv("MAILPILOT_TEST_CORE_CHILD") == "1" {
		os.Args = []string{"mailpilot-core", "--data-dir", os.Getenv("MAILPILOT_TEST_CORE_DIR")}
		os.Exit(run())
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestContractedRuntimeEntrypoints$")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "MAILPILOT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "MAILPILOT_TEST_CORE_CHILD=1", "MAILPILOT_TEST_CORE_DIR="+t.TempDir(),
		"MAILPILOT_EMBEDDING_PYTHON=/retired/missing/python", "MAILPILOT_EMBEDDING_MODEL_DIR=/retired/missing/model")
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":1}}
{"jsonrpc":"2.0","id":"ask","method":"ask.manage","params":{"operation":"prepare","accountId":"a","question":"private"}}
{"jsonrpc":"2.0","id":"search","method":"search.semantic","params":{"operation":"index","accountId":"a"}}
{"jsonrpc":"2.0","id":"draft","method":"reply.manage","params":{"operation":"create","accountId":"a","actionId":"a","previewId":"p","approved":true}}
{"jsonrpc":"2.0","id":"settings","method":"ai.settings","params":{"operation":"status"}}
{"jsonrpc":"2.0","id":"summary","method":"email.summarize","params":{"operation":"preview","accountId":"a","emailId":"selected"}}
{"jsonrpc":"2.0","id":"generic","method":"ai.execute","params":{"prompt":"private"}}
{"jsonrpc":"2.0","id":"bad-summary","method":"email.summarize","params":{"operation":"preview","accountId":"a","emailId":"selected","prompt":"private"}}
{"jsonrpc":"2.0","id":"health","method":"health","params":{}}
{"jsonrpc":"2.0","id":"stop","method":"shutdown","params":{}}
`)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("contracted core failed to start", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 10 {
		t.Fatal("missing core responses")
	}
	for i, code := range map[int]int{1: -32601, 2: -32601, 3: -32602, 6: -32601, 7: -32602} {
		var r struct{ Error struct{ Code int } }
		if json.Unmarshal([]byte(lines[i]), &r) != nil || r.Error.Code != code {
			t.Fatal("retired operation accepted", i)
		}
	}
	for _, index := range []int{4, 5} {
		if !strings.Contains(lines[index], `"code":"unauthorized"`) {
			t.Fatal("AI authorization failed closed")
		}
	}
	if !strings.Contains(lines[8], `"database":"connected"`) || !strings.Contains(diagnostics.String(), `"msg":"core stopped"`) {
		t.Fatal("retained core lifecycle failed")
	}
}
