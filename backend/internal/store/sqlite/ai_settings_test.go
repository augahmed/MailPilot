package sqlite

import (
	"context"
	"errors"
	"testing"

	"mailpilot.local/core/internal/ai"
)

func TestAISettingsAndReplyReadsPreserveCancellation(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checks := map[string]func() error{
		"settings":      func() error { _, err := s.AISettings().Settings(ctx, "account"); return err },
		"save settings": func() error { return s.AISettings().SaveSettings(ctx, "account", ai.Settings{}) },
		"get draft":     func() error { _, err := s.Replies().Get(ctx, "account", "draft"); return err },
		"latest draft":  func() error { _, err := s.Replies().Latest(ctx, "account"); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
		})
	}
}

func TestAISettingsSurviveRestartAndRemainAccountScoped(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := account(t, s, "settings-a")
	b := account(t, s, "settings-b")
	want := ai.Settings{Provider: "openai", Model: "synthetic-model", Enabled: true}
	if err := s.AISettings().SaveSettings(ctx, a, want); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.AISettings().Settings(ctx, a)
	if err != nil || got != want {
		t.Fatal("saved settings not restored", err)
	}
	other, err := reopened.AISettings().Settings(ctx, b)
	if err != nil || other.Enabled || other.Model != "" {
		t.Fatal("settings crossed accounts", err)
	}
}

func TestProviderSelectionMigrationPreservesExistingPreferenceAndHistory(t *testing.T) {
	ctx := context.Background()
	s, dir := historicalStore(t, 13)
	var err error
	defer func() { _ = s.Close() }()
	a := account(t, s, "upgrade-ai")
	plan := draftPlan(t, s, a)
	importHistoricalDraft(t, s, plan, "uncertain")
	if _, err = s.db.Exec(`INSERT INTO reply_ai_settings VALUES(?,?,1)`, a, "old-functional-model"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.AISettings().Settings(ctx, a)
	if err != nil || cfg.Provider != "openai" || cfg.Model != "old-functional-model" || !cfg.Enabled {
		t.Fatal("existing choice lost", err)
	}
	action, err := s.Replies().Get(ctx, a, plan.ID)
	if err != nil || action.Plan.Digest != plan.Digest || action.State != "uncertain" {
		t.Fatal("journal changed by provider migration", err)
	}
	if _, err = s.Emails().Get(ctx, a, plan.EmailID); err != nil {
		t.Fatal("cached mail lost", err)
	}
	cfg.Provider = "another-functional-adapter"
	if err = s.AISettings().SaveSettings(ctx, a, cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.AISettings().Settings(ctx, a)
	if err != nil || restored != cfg {
		t.Fatal("provider choice did not persist", err)
	}
}
