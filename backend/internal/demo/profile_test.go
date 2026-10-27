package demo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
	"mailpilot.local/core/internal/store/sqlite"
)

func TestProfileFencesAndIdempotentSamples(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "demo")
	if err := PrepareProfile(root, true); err != nil {
		t.Fatal(err)
	}
	if err := PrepareProfile(root, false); err == nil {
		t.Fatal("production adopted demo")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if PrepareProfile(alias, false) == nil || PrepareProfile(alias, true) == nil {
		t.Fatal("alias bypassed profile fence")
	}
	db, err := sqlite.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.VerifyProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	chunker, err := app.NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	seed := func(reset bool) {
		t.Helper()
		if err := Seed(ctx, db.Accounts(), db.Emails(), chunker.Prepare, reset); err != nil {
			t.Fatal(err)
		}
	}
	seed(false)
	for _, id := range []domain.EmailID{"demo-security-reset", "demo-security-invoice", "demo-receipt"} {
		row, err := db.Emails().Get(ctx, AccountID, id)
		if err != nil {
			t.Fatal(err)
		}
		features, err := risk.NewPhishingFeatureExtractor().Extract(ctx, row.Email)
		if err != nil {
			t.Fatal(err)
		}
		engine := risk.DefaultEngine()
		result, err := engine.Analyze(ctx, features, "synthetic-fixture-check", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if id == "demo-receipt" {
			if engine.Level(result.Score) != "LOW" {
				t.Fatal("ordinary receipt became a high-risk demonstration")
			}
			continue
		}
		if engine.Level(result.Score) != "HIGH" || features.HTMLFormCount == 0 {
			t.Fatalf("%s: synthetic evidence did not produce explained high risk: %d", id, result.Score)
		}
		codes := map[string]bool{}
		for _, finding := range result.Findings {
			codes[finding.Code] = true
		}
		if !codes["misleading_url"] || !codes["ip_url"] || !codes["credentials"] || !codes["form"] {
			t.Fatalf("%s: missing expected independent risk explanations", id)
		}
		t.Logf("%s: %s %d/100", id, engine.Level(result.Score), result.Score)
	}
	row, err := db.Emails().Get(ctx, AccountID, "demo-receipt")
	if err != nil {
		t.Fatal(err)
	}
	row.Email.BodyText = "Synthetic local edit"
	if _, err = db.Emails().Upsert(ctx, row.Email); err != nil {
		t.Fatal(err)
	}
	seed(false)
	row, _ = db.Emails().Get(ctx, AccountID, "demo-receipt")
	if row.Email.BodyText != "Synthetic local edit" {
		t.Fatal("restart overwrote changes")
	}
	seed(true)
	rows, err := db.Emails().List(ctx, AccountID, repository.Page{Limit: 100})
	if err != nil || len(rows) != len(samples) {
		t.Fatal("duplicated fixtures", err)
	}
	row, _ = db.Emails().Get(ctx, AccountID, "demo-receipt")
	if row.Email.BodyText == "Synthetic local edit" {
		t.Fatal("reset did not restore sample")
	}
	if db.VerifyProfile(ctx, false) == nil {
		t.Fatal("production accepted a copied demo database")
	}
	p := &Mailbox{Account: AccountID, Emails: db.Emails()}
	if !errors.Is(p.ApplyLabel(ctx, "sample-receipt", "label"), provider.ErrUnsupported) {
		t.Fatal("demo wrote a label")
	}
	p.Account = "foreign"
	if _, err = p.FetchMessage(ctx, "sample-receipt"); err == nil {
		t.Fatal("cross-account read accepted")
	}
	if _, err = db.Accounts().Upsert(ctx, domain.Account{ID: "real", Provider: "gmail", ProviderAccountID: "real", CredentialRef: "do-not-touch"}); err != nil {
		t.Fatal(err)
	}
	if db.VerifyProfile(ctx, true) == nil {
		t.Fatal("demo accepted real account metadata")
	}
}

func TestUnmarkedDirectoryIsNeverAdopted(t *testing.T) {
	if PrepareProfile(t.TempDir(), true) == nil {
		t.Fatal("adopted an unmarked directory")
	}
}
