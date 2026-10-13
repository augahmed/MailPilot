// Package ask constructs bounded evidence independently of retrieval and generation.
package ask

import (
	"encoding/json"
	"fmt"
	"io"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/search"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Version = "ask-context-v1"
const MaxContext = 8000

// MinimumSimilarity is a conservative candidate cutoff, not a confidence probability.
const MinimumSimilarity = 0.30

type Source struct {
	Ref        string `json:"ref"`
	EmailID    string `json:"emailId"`
	Subject    string `json:"subject"`
	Sender     string `json:"sender"`
	ReceivedAt int64  `json:"receivedAt"`
	Evidence   string `json:"evidence"`
	Hash       string `json:"-"`
}
type Claim struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Quote  string `json:"quote"`
}
type Answer struct {
	Insufficient bool    `json:"insufficient"`
	Claims       []Claim `json:"claims"`
}

func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Build never expands to unrelated thread messages. Only retrieved messages and
// bounded neighbors of the winning passage are eligible for disclosure.
func Build(account domain.AccountID, question string, result search.Result) (ai.Request, []Source, bool, error) {
	sources := []Source{}
	seen := map[string]bool{}
	truncated := result.Partial
	for _, hit := range result.Hits {
		if len(sources) == 5 {
			truncated = true
			break
		}
		e := hit.Stored.Email
		if e.AccountID != account {
			return ai.Request{}, nil, false, ai.ErrInvalid
		}
		if math.IsNaN(hit.Similarity) || math.IsInf(hit.Similarity, 0) || hit.Similarity < MinimumSimilarity {
			continue
		}
		prepared, err := embedding.PrepareRetrieval(e)
		if err != nil {
			truncated = true
			continue
		}
		body, err := embedding.RetrievalBody(e)
		if err != nil {
			truncated = true
			continue
		}
		dedupBody := body
		if dedupBody == "" {
			dedupBody = prepared.Hash
		}
		dedup := string(e.ProviderThreadID) + "\x00" + dedupBody
		if e.ProviderThreadID != "" && seen[dedup] {
			continue
		}
		start, end := 0, min(len(prepared.Passages), 4)
		if hit.PassageKind == "body" || hit.PassageKind == "subject" {
			start = max(0, hit.PassageIndex-2)
			end = min(len(prepared.Passages), hit.PassageIndex+3)
		}
		if start >= end {
			truncated = true
			continue
		}
		pieces := []string{}
		for _, p := range prepared.Passages[start:end] {
			pieces = append(pieces, p.Text)
		}
		evidence := clip(strings.Join(pieces, "\n"), 1400)
		if strings.TrimSpace(evidence) == "" {
			continue
		}
		src := Source{Ref: fmt.Sprintf("S%d", len(sources)+1), EmailID: string(e.ID), Subject: clip(e.Subject, 256), Sender: clip(e.Sender.Name+" <"+e.Sender.Address+">", 320), ReceivedAt: e.ReceivedAt.UnixMilli(), Evidence: evidence, Hash: hit.Stored.ContentHash}
		trial := append(append([]Source{}, sources...), src)
		if len(contextJSON(trial)) > MaxContext {
			truncated = true
			continue
		}
		sources = trial
		seen[dedup] = true
		truncated = truncated || start > 0 || end < len(prepared.Passages)
	}
	request := ai.Request{Instruction: ai.NewText(question), Context: ai.NewText(contextJSON(sources)), MaxOutputTokens: 1800}
	return request, sources, truncated, request.Validate()
}

// Outbound references are opaque aliases, not local database identifiers. JSON
// escaping makes attacker-controlled delimiters remain string data.
func contextJSON(sources []Source) string {
	type evidence struct {
		Source   string `json:"source"`
		Sender   string `json:"sender"`
		Subject  string `json:"subject"`
		Received string `json:"receivedUTC"`
		Passage  string `json:"passage"`
	}
	rows := []evidence{}
	for _, s := range sources {
		rows = append(rows, evidence{s.Ref, s.Sender, s.Subject, time.UnixMilli(s.ReceivedAt).UTC().Format(time.RFC3339), s.Evidence})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

// ValidateAnswer rejects the entire output if any claim lacks a known citation
// and an exact quote. Quote validation proves provenance, not entailment.
func ValidateAnswer(text string, sources []Source) (Answer, error) {
	fail := func() (Answer, error) { return Answer{Insufficient: true, Claims: []Claim{}}, ai.ErrResponse }
	var a Answer
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil {
		return fail()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fail()
	}
	if a.Insufficient {
		if len(a.Claims) != 0 {
			return fail()
		}
		a.Claims = []Claim{}
		return a, nil
	}
	if len(a.Claims) == 0 || len(a.Claims) > 6 {
		return fail()
	}
	for _, c := range a.Claims {
		if strings.TrimSpace(c.Text) == "" || len(c.Text) > 1000 || len(c.Quote) < 8 || len(c.Quote) > 700 || strings.ContainsRune(c.Text+c.Quote, '\x00') {
			return fail()
		}
		valid := false
		for _, s := range sources {
			if c.Source == s.Ref && strings.Contains(s.Evidence, c.Quote) {
				valid = true
				break
			}
		}
		if !valid {
			return fail()
		}
	}
	return a, nil
}
