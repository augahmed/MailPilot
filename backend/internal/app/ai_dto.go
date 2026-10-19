package app

import (
	"encoding/json"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"strings"
	"unicode/utf8"
)

// Incoming only: inherited JSON/diagnostic formatting always redacts the key.
type KeyInput struct{ credentials.Secret }

func (k *KeyInput) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) != nil || len(value) > 2048 {
		return ai.ErrInvalid
	}
	k.Secret = credentials.NewSecret(value)
	return nil
}
func boundedID(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

type AISettingsRequest struct {
	Operation string   `json:"operation"`
	AccountID string   `json:"accountId"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	APIKey    KeyInput `json:"apiKey"`
}

func (p AISettingsRequest) Valid() bool {
	if !boundedID(p.AccountID, 100) || !boundedID(p.Provider, 64) || !boundedID(p.Model, 128) {
		return false
	}
	switch p.Operation {
	case "status":
		return p.Provider == "" && p.Model == "" && p.APIKey.Empty()
	case "disable":
		return p.AccountID != "" && p.Provider == "" && p.Model == "" && p.APIKey.Empty()
	case "configure":
		return p.AccountID != "" && p.Provider != "" && p.Model != "" && len(p.APIKey.Reveal()) <= 2048 && strings.IndexFunc(p.APIKey.Reveal(), func(r rune) bool { return r < 33 || r > 126 }) < 0
	default:
		return false
	}
}

type AIProviderOption struct {
	ID       string `json:"id"`
	External bool   `json:"external"`
}
type AISettingsResponse struct {
	AccountID string             `json:"accountId"`
	Provider  string             `json:"provider"`
	Model     string             `json:"model"`
	Enabled   bool               `json:"enabled"`
	Providers []AIProviderOption `json:"providers"`
	Code      string             `json:"code"`
}
type SummarizeEmailRequest struct {
	Operation string `json:"operation"`
	AccountID string `json:"accountId"`
	EmailID   string `json:"emailId"`
	PreviewID string `json:"previewId"`
	JobID     string `json:"jobId"`
	Approved  bool   `json:"approved"`
}

func (p SummarizeEmailRequest) Valid() bool {
	for _, value := range []string{p.AccountID, p.EmailID, p.PreviewID, p.JobID} {
		if !boundedID(value, 100) {
			return false
		}
	}
	if p.AccountID == "" || p.EmailID == "" {
		return false
	}
	switch p.Operation {
	case "preview", "status", "clear":
		return p.PreviewID == "" && p.JobID == "" && !p.Approved
	case "approve":
		return p.PreviewID != "" && p.JobID == "" && p.Approved
	case "cancel":
		return p.PreviewID == "" && p.JobID != "" && !p.Approved
	default:
		return false
	}
}

type SummaryResultDTO struct {
	Summary   string   `json:"summary"`
	Requests  []string `json:"requests"`
	Deadlines []string `json:"deadlines"`
}
type SummarizeEmailResponse struct {
	AccountID string            `json:"accountId"`
	EmailID   string            `json:"emailId"`
	State     string            `json:"state"`
	JobID     string            `json:"jobId"`
	PreviewID string            `json:"previewId"`
	ExpiresAt int64             `json:"expiresAt"`
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	External  bool              `json:"external"`
	Email     string            `json:"email"`
	Policy    string            `json:"policy"`
	Truncated bool              `json:"truncated"`
	Result    *SummaryResultDTO `json:"result"`
	Code      string            `json:"code"`
}

func emptySummary() SummarizeEmailResponse { return SummarizeEmailResponse{State: "idle"} }

func boundSummary(r SummarizeEmailResponse) SummarizeEmailResponse {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 48*1024 {
		result := emptySummary()
		result.State = "error"
		result.Code = "response_too_large"
		return result
	}
	return r
}
