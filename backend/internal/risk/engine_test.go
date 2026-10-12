package risk

import (
	"context"
	"encoding/json"
	"mailpilot.local/core/internal/domain"
	"os"
	"testing"
	"time"
)

func TestEngineFixtures(t *testing.T) {
	for _, name := range []string{"legitimate", "suspicious", "malformed"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var mail domain.Email
			if err = json.Unmarshal(b, &mail); err != nil {
				t.Fatal(err)
			}
			f, err := NewPhishingFeatureExtractor().Extract(context.Background(), mail)
			if err != nil {
				t.Fatal(err)
			}
			e := DefaultEngine()
			p, err := e.Analyze(context.Background(), f, "hash", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if p.Score < 0 || p.Score > 100 {
				t.Fatal(p.Score)
			}
			if name == "legitimate" && e.Level(p.Score) != "LOW" {
				t.Fatal(p)
			}
			if name == "suspicious" && e.Level(p.Score) != "HIGH" {
				t.Fatal(p)
			}
			if name == "malformed" {
				found := false
				for _, r := range p.Findings {
					found = found || r.Code == "incomplete"
				}
				if !found {
					t.Fatal("missing incomplete explanation")
				}
			}
		})
	}
}
func TestEngineSignals(t *testing.T) {
	base := domain.PhishingFeatures{ExtractorVersion: ExtractorVersion, BodyState: "complete", FromDomain: "example.com"}
	tests := []struct {
		name   string
		mutate func(*domain.PhishingFeatures)
		score  int
	}{
		{"spoof", func(f *domain.PhishingFeatures) {
			f.ReplyToDomain = "attacker.test"
			f.LanguageIndicators = []domain.PhishingLanguageIndicator{{Kind: "credentials"}}
		}, 40},
		{"misleading", func(f *domain.PhishingFeatures) { f.URLs = []domain.PhishingURL{{CrossSiteMismatch: true}} }, 25},
		{"duplicate", func(f *domain.PhishingFeatures) { f.URLs = []domain.PhishingURL{{IPAddress: true}, {IPAddress: true}} }, 15},
		{"contradictory auth", func(f *domain.PhishingFeatures) {
			f.DMARCResult = "conflicting"
			f.DMARCResults = []string{"pass", "fail"}
			f.ReplyToDomain = "attacker.test"
		}, 25},
		{"forged pass", func(f *domain.PhishingFeatures) { f.DMARCResult = "pass"; f.ReplyToDomain = "attacker.test" }, 25},
		{"weak legitimate reduction", func(f *domain.PhishingFeatures) {
			f.URLs = []domain.PhishingURL{{Domain: "www.example.com", Scheme: "https"}}
			f.LanguageIndicators = []domain.PhishingLanguageIndicator{{Kind: "credentials"}}
		}, 7},
		{"incomplete no reduction", func(f *domain.PhishingFeatures) {
			f.Incomplete = true
			f.URLs = []domain.PhishingURL{{Domain: "example.com", Scheme: "https"}}
			f.LanguageIndicators = []domain.PhishingLanguageIndicator{{Kind: "credentials"}}
		}, 15},
	}
	e := DefaultEngine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := base
			tt.mutate(&f)
			p, err := e.Analyze(context.Background(), f, "hash", time.Now())
			if err != nil || p.Score != tt.score {
				t.Fatalf("score %d want %d: %v", p.Score, tt.score, err)
			}
			for i := 1; i < len(p.Findings); i++ {
				if p.Findings[i].Points > 0 && p.Findings[i].Points > p.Findings[i-1].Points {
					t.Fatal("unordered")
				}
			}
		})
	}
	for _, x := range []struct {
		score int
		level string
	}{{0, "LOW"}, {29, "LOW"}, {30, "MEDIUM"}, {59, "MEDIUM"}, {60, "HIGH"}, {100, "HIGH"}} {
		if e.Level(x.score) != x.level {
			t.Fatal(x)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Analyze(ctx, base, "hash", time.Now()); err == nil {
		t.Fatal("ignored cancellation")
	}
	c := DefaultConfig()
	custom, err := NewEngine(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Weights["credentials"] = 100
	if custom.Version() != e.Version() {
		t.Fatal("mutated config")
	}
	changed, _ := NewEngine(c)
	if changed.Version() == e.Version() {
		t.Fatal("missing config invalidation")
	}
	c.High = c.Medium
	if _, err := NewEngine(c); err == nil {
		t.Fatal("invalid thresholds")
	}
}
