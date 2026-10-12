package app

import (
	"encoding/json"

	"mailpilot.local/core/internal/domain"
)

type OrganizationRequest struct {
	Source    string   `json:"source,omitempty"`
	Operation string   `json:"operation"`
	AccountID string   `json:"accountId"`
	Offset    int      `json:"offset"`
	EmailIDs  []string `json:"emailIds"`
	ActionID  string   `json:"actionId"`
	Digest    string   `json:"digest"`
}

func (p OrganizationRequest) Valid() bool {
	if p.Source != "" && (p.Source != "phishing" || (p.Operation != "list" && p.Operation != "preview" && p.Operation != "dry_run")) {
		return false
	}
	if len(p.AccountID) > 100 || p.Offset < 0 || p.Offset > 1000000 || len(p.EmailIDs) > 5 || len(p.ActionID) > 100 || len(p.Digest) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range p.EmailIDs {
		if id == "" || len(id) > 100 || seen[id] {
			return false
		}
		seen[id] = true
	}
	switch p.Operation {
	case "list", "status":
		return len(p.EmailIDs) == 0 && p.Digest == ""
	case "preview", "dry_run":
		return p.AccountID != "" && len(p.EmailIDs) > 0 && p.ActionID == "" && p.Digest == ""
	case "undo_preview":
		return p.AccountID != "" && len(p.EmailIDs) == 0 && p.ActionID == "" && p.Digest == ""
	case "apply":
		return p.AccountID != "" && p.ActionID != "" && len(p.Digest) == 64 && len(p.EmailIDs) == 0
	case "reconcile", "cancel":
		return p.AccountID != "" && len(p.EmailIDs) == 0 && p.Digest == ""
	default:
		return false
	}
}

type OrganizationRiskDTO struct {
	*RiskDTO
	Threshold int `json:"threshold"`
}

func organizationRiskDTO(p *domain.OrganizationRisk) *OrganizationRiskDTO {
	if p == nil {
		return nil
	}
	return &OrganizationRiskDTO{riskDTO(p.Score, "HIGH", p.Version, p.Findings), p.Threshold}
}

type OrganizationRowDTO struct {
	Phishing      *OrganizationRiskDTO `json:"phishing,omitempty"`
	EmailID       string               `json:"emailId"`
	Sender        string               `json:"sender"`
	Subject       string               `json:"subject"`
	CurrentLabels []string             `json:"currentLabels"`
	ProposedLabel string               `json:"proposedLabel"`
	Confidence    float64              `json:"confidence"`
	Change        string               `json:"change"`
}
type OrganizationStepDTO struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	EmailID string `json:"emailId"`
	State   string `json:"state"`
	Code    string `json:"code"`
}
type OrganizationResponse struct {
	AccountID string                `json:"accountId"`
	State     string                `json:"state"`
	Code      string                `json:"code"`
	ID        string                `json:"id"`
	Digest    string                `json:"digest"`
	Kind      string                `json:"kind"`
	ExpiresAt int64                 `json:"expiresAt"`
	DryRun    bool                  `json:"dryRun"`
	Rows      []OrganizationRowDTO  `json:"rows"`
	Steps     []OrganizationStepDTO `json:"steps"`
	HasMore   bool                  `json:"hasMore"`
}

func emptyOrganization() OrganizationResponse {
	return OrganizationResponse{State: "idle", Rows: []OrganizationRowDTO{}, Steps: []OrganizationStepDTO{}}
}

// Account-scoped display fields are hostile input. Bound the encoded response,
// including JSON escaping, before it reaches the native frame reader.
func boundedOrganization(r OrganizationResponse) OrganizationResponse {
	data, err := json.Marshal(r)
	if err != nil || len(data) > 48*1024 {
		limited := emptyOrganization()
		limited.AccountID = r.AccountID
		limited.Code = "validation"
		return limited
	}
	return r
}
func organizationDTO(p domain.OrganizationPlan) OrganizationResponse {
	r := emptyOrganization()
	r.AccountID = string(p.AccountID)
	r.State = string(p.Status)
	r.ID = string(p.ID)
	r.Digest = p.Digest
	r.Kind = string(p.Kind)
	r.ExpiresAt = p.ExpiresAt
	for _, row := range p.Rows {
		r.Rows = append(r.Rows, OrganizationRowDTO{organizationRiskDTO(row.Phishing), string(row.EmailID), row.Sender, row.Subject, row.CurrentLabels, row.ProposedLabel, row.Confidence, row.Change})
	}
	for _, step := range p.Steps {
		r.Steps = append(r.Steps, OrganizationStepDTO{step.Kind, step.Name, string(step.EmailID), string(step.State), step.Code})
	}
	return boundedOrganization(r)
}
