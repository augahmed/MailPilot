package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/config"
	"mailpilot.local/core/internal/demo"
)

type forbiddenDemoTransport struct{}

func (forbiddenDemoTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("demo attempted network access")
}

func TestOfflineDemoComposition(t *testing.T) {
	if os.Getenv("MAILPILOT_TEST_DEMO_CHILD") == "1" {
		http.DefaultTransport = forbiddenDemoTransport{}
		keyring.MockInitWithError(errors.New("demo must not use credentials"))
		args := []string{"--demo"}
		if os.Getenv("MAILPILOT_TEST_DEMO_RESET") == "1" {
			args = append(args, "--reset-demo")
		}
		cfg, err := config.Load(args, os.LookupEnv, filepath.Join(os.Getenv("MAILPILOT_TEST_DEMO_ROOT"), "production"))
		if err != nil {
			os.Exit(2)
		}
		os.Exit(runConfigured(cfg))
	}
	root := t.TempDir()
	var previousPlan, previousDigest string
	for pass := 0; pass < 3; pass++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOfflineDemoComposition$")
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "MAILPILOT_") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "MAILPILOT_TEST_DEMO_CHILD=1", "MAILPILOT_TEST_DEMO_ROOT="+root, "MAILPILOT_DATA_DIR="+filepath.Join(root, "production"), "MAILPILOT_GOOGLE_CLIENT_ID=invalid-ignored-in-demo", "MAILPILOT_GOOGLE_CLIENT_SECRET=ignored")
		if pass == 2 {
			cmd.Env = append(cmd.Env, "MAILPILOT_TEST_DEMO_RESET=1")
		}
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		cmd.Stderr = &logs
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		scan := bufio.NewScanner(output)
		scan.Buffer(make([]byte, 4096), 64*1024)
		id := 0
		call := func(method string, params any) map[string]any {
			t.Helper()
			id++
			if err := json.NewEncoder(input).Encode(map[string]any{"jsonrpc": "2.0", "id": fmt.Sprint(id), "method": method, "params": params}); err != nil {
				t.Fatal(err)
			}
			if !scan.Scan() {
				t.Fatal("demo core stopped before response", method)
			}
			var response struct {
				Result map[string]any
				Error  any
			}
			if json.Unmarshal(scan.Bytes(), &response) != nil || response.Error != nil {
				t.Fatal("demo RPC failed", method, string(scan.Bytes()))
			}
			return response.Result
		}
		call("initialize", map[string]int{"protocolVersion": 1})
		status := call("auth.status", map[string]any{})
		if status["demo"] != true || status["state"] != "connected" {
			t.Fatal("missing synthetic session")
		}
		if call("auth.begin", map[string]any{})["authorizationUrl"] != nil {
			t.Fatal("demo offered Google sign-in")
		}
		page := call("mail.list", map[string]int{"offset": 0})
		if len(page["rows"].([]any)) != 10 {
			t.Fatal("missing sample mailbox")
		}
		page = call("mail.list", map[string]int{"offset": 10})
		if len(page["rows"].([]any)) != 4 {
			t.Fatal("samples duplicated across restart/reset")
		}
		account := string(demo.AccountID)
		if pass > 0 {
			prior := call("organization.manage", app.OrganizationRequest{Operation: "status", AccountID: account})
			if prior["id"] != previousPlan || prior["digest"] != previousDigest {
				t.Fatal("restart/reset changed immutable preview evidence")
			}
		}
		if call("index.local", app.IndexRequest{Operation: "status", AccountID: account})["indexedMessages"] != float64(0) {
			t.Fatal("startup inferred demo vectors")
		}
		if call("sync.start", map[string]any{})["state"] != "error" {
			t.Fatal("demo permitted sync")
		}
		if call("ai.settings", map[string]string{"operation": "status", "accountId": account})["code"] != "ai_disabled" {
			t.Fatal("demo permitted AI")
		}
		if call("email.summarize", app.SummarizeEmailRequest{Operation: "preview", AccountID: account, EmailID: "demo-receipt"})["code"] != "ai_disabled" {
			t.Fatal("demo permitted summary")
		}
		if call("mail.read", app.ReadMailRequest{AccountID: "foreign", EmailID: "demo-receipt"})["code"] != "unauthorized" {
			t.Fatal("cross-profile reader accepted")
		}
		result := call("search.local", app.SearchRequest{Operation: "start", AccountID: account, Query: "MP-1042", Limit: 10, Mode: "keyword"})
		for result["status"] == "running" {
			time.Sleep(time.Millisecond)
			result = call("search.local", app.SearchRequest{Operation: "status", AccountID: account})
		}
		if result["code"] != "" || len(result["emails"].([]any)) == 0 {
			t.Fatal("offline keyword search failed", result["code"])
		}
		foreign := call("search.local", app.SearchRequest{Operation: "start", AccountID: "foreign", Query: "MP-1042", Limit: 10, Mode: "keyword"})
		if foreign["code"] != "unauthorized" {
			t.Fatal("cross-profile search accepted")
		}
		call("classification.review", app.ClassificationRequest{Operation: "run", AccountID: account})
		preview := call("organization.manage", app.OrganizationRequest{Operation: "preview", AccountID: account, EmailIDs: []string{"demo-application"}})
		for preview["state"] == "running" {
			time.Sleep(time.Millisecond)
			preview = call("organization.manage", app.OrganizationRequest{Operation: "status", AccountID: account})
		}
		if preview["code"] != nil && preview["code"] != "" {
			t.Fatal("local label preview failed", preview["code"])
		}
		if preview["id"] == "" || preview["digest"] == "" {
			t.Fatal("missing real local label plan", preview["state"])
		}
		if call("organization.manage", app.OrganizationRequest{Operation: "apply", AccountID: account, ActionID: preview["id"].(string), Digest: preview["digest"].(string)})["code"] != "unsupported" {
			t.Fatal("demo permitted label dispatch")
		}
		previousPlan, previousDigest = preview["id"].(string), preview["digest"].(string)
		call("shutdown", map[string]any{})
		input.Close()
		if err = cmd.Wait(); err != nil {
			t.Fatal("demo lifecycle failed", err, logs.String())
		}
		cancel()
		if _, err = os.Stat(filepath.Join(root, "production")); !os.IsNotExist(err) {
			t.Fatal("demo touched production profile")
		}
	}
}
