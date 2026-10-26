package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestGoogleDefaultAndEnvironmentAreAtomic(t *testing.T) {
	packaged := []byte(`{"client_id":"packaged.apps.googleusercontent.com","client_secret":"synthetic-packaged"}`)
	for _, tc := range []struct {
		name       string
		env        map[string]string
		id, secret string
		bad        bool
	}{
		{"default", nil, "packaged.apps.googleusercontent.com", "synthetic-packaged", false},
		{"override", map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "override.apps.googleusercontent.com", "MAILPILOT_GOOGLE_CLIENT_SECRET": "synthetic-override"}, "override.apps.googleusercontent.com", "synthetic-override", false},
		{"disabled", map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "", "MAILPILOT_GOOGLE_CLIENT_SECRET": ""}, "", "", false},
		{"id-only", map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "override.apps.googleusercontent.com"}, "", "", true},
		{"secret-only", map[string]string{"MAILPILOT_GOOGLE_CLIENT_SECRET": "synthetic-override"}, "", "", true},
		{"empty-half", map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "", "MAILPILOT_GOOGLE_CLIENT_SECRET": "synthetic-override"}, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, secret, err := googleConfiguration(environment(tc.env), packaged)
			if (err != nil) != tc.bad || id != tc.id || secret.Reveal() != tc.secret {
				t.Fatal("incorrect default/override isolation")
			}
		})
	}
	if id, secret, err := googleConfiguration(noEnvironment, nil); err != nil || id != "" || !secret.Empty() {
		t.Fatal("standard build configured OAuth")
	}
}

func TestInvalidPackagedGoogleConfigurationFailsClosed(t *testing.T) {
	for _, raw := range []string{
		"", "{", "null", "{}", strings.Repeat("x", 4097),
		`{"client_id":"bad","client_secret":"synthetic"}`,
		`{"client_id":"synthetic.apps.googleusercontent.com","client_secret":""}`,
		`{"client_id":"synthetic.apps.googleusercontent.com","client_secret":"sk-synthetic-key"}`,
		`{"client_id":"synthetic.apps.googleusercontent.com","client_secret":"synthetic","token_uri":"https://attacker.test"}`,
		`{"refresh_token":"synthetic-user-token","client_secret":"synthetic"}`,
		`{"installed":{"client_id":"synthetic.apps.googleusercontent.com","client_secret":"synthetic"}}`,
	} {
		// Even an explicit disabling override cannot hide a corrupt package.
		_, _, err := googleConfiguration(environment(map[string]string{"MAILPILOT_GOOGLE_CLIENT_ID": "", "MAILPILOT_GOOGLE_CLIENT_SECRET": ""}), []byte(raw))
		if err != errGoogleConfig {
			t.Fatal("invalid packaged configuration accepted")
		}
		if strings.Contains(err.Error(), "synthetic-user-token") {
			t.Fatal("configuration error exposed input")
		}
	}
}

func TestEmbeddedGoogleDefaultLoadsWithoutEnvironment(t *testing.T) {
	cfg, err := Load(nil, noEnvironment, t.TempDir())
	if err != nil {
		t.Fatal("embedded default failed validation")
	}
	if packagedGoogleJSON() == nil {
		if cfg.GoogleClientID != "" || !cfg.GoogleClientSecret.Empty() {
			t.Fatal("standard build inherited personal configuration")
		}
	} else if cfg.GoogleClientID == "" || cfg.GoogleClientSecret.Empty() {
		t.Fatal("personal build required environment configuration")
	}
	// Public client configuration is still excluded from ordinary secret diagnostics.
	if !cfg.GoogleClientSecret.Empty() {
		if strings.Contains(fmt.Sprintf("%+v", cfg), cfg.GoogleClientSecret.Reveal()) {
			t.Fatal("secret diagnostic leak")
		}
		raw, err := json.Marshal(cfg)
		if err != nil || strings.Contains(string(raw), cfg.GoogleClientSecret.Reveal()) {
			t.Fatal("secret JSON leak")
		}
	}
}
