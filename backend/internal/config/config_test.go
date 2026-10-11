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
