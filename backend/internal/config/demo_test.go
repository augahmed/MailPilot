package config

import (
	"path/filepath"
	"testing"
)

func TestDemoStartupFencesProfileAndSkipsCredentials(t *testing.T) {
	base := filepath.Join(t.TempDir(), "production")
	env := func(key string) (string, bool) {
		if key == "MAILPILOT_GOOGLE_CLIENT_ID" || key == "MAILPILOT_GOOGLE_CLIENT_SECRET" {
			t.Fatal("demo consulted OAuth environment")
		}
		if key == "MAILPILOT_DATA_DIR" {
			return base, true
		}
		return "", false
	}
	cfg, err := Load([]string{"--demo", "--reset-demo"}, env, base)
	if err != nil || !cfg.Demo || !cfg.ResetDemo || cfg.DataDir != base+".demo-v1" || cfg.GoogleClientID != "" || !cfg.GoogleClientSecret.Empty() {
		t.Fatal("demo was not isolated", err)
	}
	for _, args := range [][]string{{"--reset-demo"}, {"--demo", "--data-dir", base}, {"--data-dir", base + ".demo-v1"}} {
		if _, err := Load(args, noEnvironment, base); err == nil {
			t.Fatal("profile/reset path fence bypassed")
		}
	}
}
