package config

import (
	"log/slog"
	"path/filepath"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	base := t.TempDir()
	env := func(key string) string {
		return map[string]string{"MAILPILOT_DATA_DIR": filepath.Join(base, "env"), "MAILPILOT_LOG_LEVEL": "warn"}[key]
	}
	c, err := Load(nil, env, base)
	if err != nil || c.DataDir != filepath.Join(base, "env") || c.LogLevel != slog.LevelWarn {
		t.Fatalf("environment config: %+v, %v", c, err)
	}
	c, err = Load([]string{"--data-dir", base, "--log-level", "debug"}, env, "")
	if err != nil || c.DataDir != base || c.LogLevel != slog.LevelDebug {
		t.Fatalf("flag config: %+v, %v", c, err)
	}
	c, err = Load(nil, func(string) string { return "" }, base)
	if err != nil || c.DataDir != base || c.LogLevel != slog.LevelInfo {
		t.Fatalf("default config: %+v, %v", c, err)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--data-dir", "relative"}, {"--data-dir", ""}, {"--log-level", "verbose"}, {"--unknown"}, {"extra"}} {
		t.Run(args[0]+args[len(args)-1], func(t *testing.T) {
			if _, err := Load(args, func(string) string { return "" }, t.TempDir()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestGoogleOAuthConfiguration(t *testing.T) {
	env := map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "synthetic.apps.googleusercontent.com", "MAILPILOT_GOOGLE_CLIENT_SECRET": "synthetic-client-secret"}
	cfg, err := Load(nil, func(k string) string { return env[k] }, t.TempDir())
	if err != nil || cfg.GoogleClientID != env["MAILPILOT_GOOGLE_CLIENT_ID"] || cfg.GoogleClientSecret.Empty() {
		t.Fatal("OAuth configuration not loaded")
	}
	env["MAILPILOT_GOOGLE_CLIENT_ID"] = "https://attacker.example/client"
	if _, err = Load(nil, func(k string) string { return env[k] }, t.TempDir()); err == nil {
		t.Fatal("invalid client accepted")
	}
}

func TestSyncLimit(t *testing.T) {
	for _, value := range []string{"0", "10001", "bad"} {
		if _, err := Load(nil, func(k string) string {
			if k == "MAILPILOT_SYNC_LIMIT" {
				return value
			}
			return ""
		}, t.TempDir()); err == nil {
			t.Fatal("invalid sync limit accepted")
		}
	}
	c, err := Load([]string{"--sync-limit", "25"}, func(string) string { return "" }, t.TempDir())
	if err != nil || c.InitialSyncLimit != 25 {
		t.Fatal("sync limit flag ignored", err)
	}
}

func TestRiskHighThreshold(t *testing.T) {
	for _, value := range []string{"30", "101", "bad"} {
		_, err := Load(nil, func(k string) string {
			if k == "MAILPILOT_RISK_HIGH_THRESHOLD" {
				return value
			}
			return ""
		}, t.TempDir())
		if err == nil {
			t.Fatal("invalid risk threshold")
		}
	}
	env := func(k string) string {
		if k == "MAILPILOT_RISK_HIGH_THRESHOLD" {
			return "80"
		}
		return ""
	}
	c, err := Load(nil, env, t.TempDir())
	if err != nil || c.RiskHighThreshold != 80 {
		t.Fatal("env threshold", err)
	}
	c, err = Load([]string{"--risk-high-threshold", "70"}, env, t.TempDir())
	if err != nil || c.RiskHighThreshold != 70 {
		t.Fatal("flag threshold", err)
	}
	c, err = Load(nil, func(string) string { return "" }, t.TempDir())
	if err != nil || c.RiskHighThreshold != 60 {
		t.Fatal("default threshold", err)
	}
}

func TestRetiredModelSetupCannotBeConfigured(t *testing.T) {
	for _, flag := range []string{"--embedding-python", "--embedding-model-dir"} {
		if _, err := Load([]string{flag, "/unused"}, func(string) string { return "" }, t.TempDir()); err == nil {
			t.Fatal("retired model flag accepted", flag)
		}
	}
	// Stale environment settings no longer enable or validate a model worker.
	if _, err := Load(nil, func(k string) string {
		if k == "MAILPILOT_EMBEDDING_PYTHON" || k == "MAILPILOT_EMBEDDING_MODEL_DIR" {
			t.Fatal("retired environment setting read")
		}
		return ""
	}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
