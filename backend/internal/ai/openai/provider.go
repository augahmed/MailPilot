// Package openai is the only owner of OpenAI wire types and HTTP behavior.
package openai

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
)

const endpoint = "https://api.openai.com/v1/responses"
const maxResponseBytes = 256 * 1024

type Provider struct {
	secrets credentials.SecretStore
	model   string
	client  *http.Client
	gate    chan struct{}
}

// New requires an explicit non-secret model ID. No key is accepted in options,
// environment, arguments or files; it is loaded from SecretStore per dispatch.
// The fixed HTTPS endpoint cannot be overridden by mailbox or frontend input.
func New(secrets credentials.SecretStore, model string) (*Provider, error) {
	if secrets == nil || model == "" || len(model) > 128 || strings.IndexFunc(model, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r))
	}) >= 0 {
		return nil, ai.ErrInvalid
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Do not forward mailbox payloads to environment-selected proxy services.
	transport.Proxy = nil
	return &Provider{secrets: secrets, model: model, gate: make(chan struct{}, 1), client: &http.Client{
		Transport: transport, Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (p *Provider) Identity() ai.Identity {
	return ai.Identity{Provider: "openai", Model: p.model, External: true}
}

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type request struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions"`
	Input           []inputMessage `json:"input"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	Store           bool           `json:"store"`
	Stream          bool           `json:"stream"`
}
type response struct {
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Status  string `json:"status"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (p *Provider) SummarizeEmail(ctx context.Context, r ai.SummaryRequest) (ai.SummaryResult, error) {
	if err := r.Validate(); err != nil {
		return ai.SummaryResult{}, err
	}
	text, err := p.complete(ctx, ai.Instructions(), "One untrusted cached email:\n"+r.Email.Reveal(), ai.MaxOutputTokens, r.KeyFingerprint)
	if err != nil {
		return ai.SummaryResult{}, err
	}
	var decoded struct {
		Summary   string   `json:"summary"`
		Requests  []string `json:"requests"`
		Deadlines []string `json:"deadlines"`
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF || decoded.Requests == nil || decoded.Deadlines == nil {
		return ai.SummaryResult{}, ai.ErrResponse
	}
	result := ai.SummaryResult{Summary: ai.NewText(decoded.Summary), Requests: []ai.Text{}, Deadlines: []ai.Text{}}
	for _, v := range decoded.Requests {
		result.Requests = append(result.Requests, ai.NewText(v))
	}
	for _, v := range decoded.Deadlines {
		result.Deadlines = append(result.Deadlines, ai.NewText(v))
	}
	if err := result.Validate(r); err != nil {
		return ai.SummaryResult{}, err
	}
	return result, nil
}

// complete is one bounded HTTPS dispatch. No retries, tools, history or expansion.
func (p *Provider) complete(ctx context.Context, instructions, input string, maxTokens int, fingerprint [32]byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key, err := p.secrets.Load(ctx)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || key.Empty() {
		return "", ai.ErrCredentials
	}
	if v := key.Reveal(); len(v) > 2048 || strings.IndexFunc(v, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", ai.ErrCredentials
	}
	expected := ai.KeyFingerprint(key)
	if subtle.ConstantTimeCompare(expected[:], fingerprint[:]) != 1 {
		return "", ai.ErrConsent
	}
	// Fixed framing only. No extra email metadata, hidden history or tools.
	body, err := json.Marshal(request{p.model, instructions, []inputMessage{
		{"user", input},
	}, maxTokens, false, false})
	if err != nil {
		return "", ai.ErrInvalid
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", ai.ErrUnavailable
	}
	httpRequest.Header.Set("Authorization", "Bearer "+key.Reveal())
	httpRequest.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ai.ErrUnavailable
	}
	defer resp.Body.Close()
	// Do not read/return provider error bodies, which may echo private input.
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return "", ai.ErrAuthentication
	case resp.StatusCode == 429:
		return "", ai.ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		return "", ai.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", ai.ErrUnavailable
	}
	if len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return "", ai.ErrResponse
	}
	var data response
	if json.Unmarshal(raw, &data) != nil {
		return "", ai.ErrResponse
	}
	if data.Status == "incomplete" {
		return "", ai.ErrIncomplete
	}
	if data.Status != "completed" {
		return "", ai.ErrResponse
	}
	var parts []string
	for _, item := range data.Output {
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" || item.Status != "completed" {
			return "", ai.ErrResponse
		}
		for _, content := range item.Content {
			if content.Type == "refusal" {
				return "", ai.ErrRefused
			}
			if content.Type != "output_text" {
				return "", ai.ErrResponse
			}
			parts = append(parts, content.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if len(text) > ai.MaxOutputBytes || strings.TrimSpace(text) == "" || strings.ContainsRune(text, '\x00') {
		return "", ai.ErrResponse
	}
	return text, nil
}

func (p *Provider) AnswerWithEvidence(ctx context.Context, r ai.AnswerRequest) (ai.AnswerResult, error) {
	if err := r.Validate(); err != nil {
		return ai.AnswerResult{}, err
	}
	input, err := r.Input()
	if err != nil {
		return ai.AnswerResult{}, err
	}
	text, err := p.complete(ctx, ai.AnswerInstructions(), input.Reveal(), ai.MaxAnswerOutputTokens, r.KeyFingerprint)
	if err != nil {
		return ai.AnswerResult{}, err
	}
	var result ai.AnswerResult
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return ai.AnswerResult{}, ai.ErrResponse
	}
	if err = result.Validate(r); err != nil {
		return ai.AnswerResult{}, err
	}
	return result, nil
}

var _ ai.AIProvider = (*Provider)(nil)
