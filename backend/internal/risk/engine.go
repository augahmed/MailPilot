package risk

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"mailpilot.local/core/internal/domain"
)

// Config is trusted application configuration, never message-controlled.
// Every configuration is fingerprinted into the persisted analyzer version.
type Config struct {
	Weights map[string]int
	Medium  int
	High    int
}
type rule struct {
	code, explanation string
	weight            int
}

var rules = []rule{
	{"reply_mismatch", "Reply-To and sender use different sites", 25},
	{"misleading_url", "Displayed URL and destination use different sites", 25},
	{"ip_url", "Link destination uses an IP address", 15},
	{"structured_url", "Link contains potentially misleading URL structure", 12},
	{"credentials", "Credential-related language (may also be legitimate)", 15},
	{"urgency", "Urgency language", 8},
	{"account_security", "Account-security language", 7},
	{"executable", "Executable attachment filename", 25},
	{"form", "HTML form present", 10},
	{"aligned_links", "All observed web links align with the sender site (weak signal)", -8},
}

func DefaultConfig() Config {
	c := Config{Weights: map[string]int{}, Medium: 30, High: 60}
	for _, r := range rules {
		c.Weights[r.code] = r.weight
	}
	return c
}

type Engine struct {
	config  Config
	version string
}

func NewEngine(c Config) (*Engine, error) {
	if c.Medium < 1 || c.High <= c.Medium || c.High > 100 || len(c.Weights) != len(rules) {
		return nil, errors.New("invalid risk configuration")
	}
	copy := Config{Weights: map[string]int{}, Medium: c.Medium, High: c.High}
	for _, r := range rules {
		w, ok := c.Weights[r.code]
		if !ok || w < -20 || w > 100 || (r.weight > 0 && w < 0) || (r.weight < 0 && w > 0) {
			return nil, errors.New("invalid risk weight")
		}
		copy.Weights[r.code] = w
	}
	b, _ := json.Marshal(copy)
	return &Engine{copy, fmt.Sprintf("risk-1/%s/%x", ExtractorVersion, sha256.Sum256(b))}, nil
}
func DefaultEngine() *Engine         { e, _ := NewEngine(DefaultConfig()); return e }
func (e *Engine) HighThreshold() int { return e.config.High }
func (e *Engine) Version() string    { return e.version }
func (e *Engine) Level(score int) string {
	if score >= e.config.High {
		return "HIGH"
	}
	if score >= e.config.Medium {
		return "MEDIUM"
	}
	return "LOW"
}

// Analyze consumes bounded extractor observations. Authentication headers are
// unverified claims: neither a pass nor a failure changes the score.
func (e *Engine) Analyze(ctx context.Context, f domain.PhishingFeatures, inputHash string, at time.Time) (domain.PhishingResult, error) {
	p := domain.PhishingResult{AccountID: f.AccountID, EmailID: f.EmailID, InputHash: inputHash, AnalyzerVersion: e.version, AssessedAt: at, Findings: []domain.PhishingFinding{}}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if f.ExtractorVersion != ExtractorVersion || len(f.URLs) > maxURLs || len(f.LanguageIndicators) > 1000 {
		return p, errors.New("invalid risk features")
	}
	hits := map[string]bool{}
	hits["reply_mismatch"] = f.FromDomain != "" && f.ReplyToDomain != "" && registrableDomain(f.FromDomain) != registrableDomain(f.ReplyToDomain)
	aligned := len(f.URLs) > 0 && f.FromDomain != "" && !f.Incomplete && f.BodyState == "complete" && len(f.NonWebLinks) == 0 && f.RelativeURLCount == 0
	for _, u := range f.URLs {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		hits["misleading_url"] = hits["misleading_url"] || u.CrossSiteMismatch
		hits["ip_url"] = hits["ip_url"] || u.IPAddress
		hits["structured_url"] = hits["structured_url"] || len(u.SuspiciousStructures) > 0
		if u.Malformed || u.IPAddress || u.Punycode || u.VisibleLinkMismatch || len(u.SuspiciousStructures) > 0 || len(u.RedirectDomains) > 0 || u.Domain == "" || u.Scheme != "https" || registrableDomain(u.Domain) != registrableDomain(f.FromDomain) {
			aligned = false
		}
	}
	for _, l := range f.LanguageIndicators {
		switch l.Kind {
		case "credentials", "urgency", "account_security":
			hits[l.Kind] = true
		}
	}
	hits["executable"] = len(f.ExecutableAttachments) > 0
	hits["form"] = f.HTMLFormCount > 0
	hits["aligned_links"] = aligned && !hits["reply_mismatch"] && !hits["executable"] && !hits["form"]
	for _, r := range rules {
		if hits[r.code] && e.config.Weights[r.code] != 0 {
			w := e.config.Weights[r.code]
			p.Score += w
			p.Findings = append(p.Findings, domain.PhishingFinding{Code: r.code, Explanation: r.explanation, Points: w})
		}
	}
	sort.SliceStable(p.Findings, func(i, j int) bool { return p.Findings[i].Points > p.Findings[j].Points })
	p.Score = max(0, min(100, p.Score))
	p.Findings = append(p.Findings, domain.PhishingFinding{Code: "authentication_unknown", Explanation: "Authentication is unverified; header claims do not affect this score"})
	if f.Incomplete || f.BodyState != "complete" {
		p.Findings = append(p.Findings, domain.PhishingFinding{Code: "incomplete", Explanation: "Analysis is incomplete; missing or malformed content may hide risk"})
	}
	return p, nil
}
