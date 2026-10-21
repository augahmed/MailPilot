package app

import (
	"encoding/json"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

type AnswerRequest struct {
	Operation string                  `json:"operation"`
	AccountID string                  `json:"accountId"`
	Question  string                  `json:"question"`
	Filter    repository.SearchFilter `json:"filter"`
	JobID     string                  `json:"jobId"`
	PreviewID string                  `json:"previewId"`
	Digest    string                  `json:"digest"`
	SourceIDs []string                `json:"sourceIds"`
	Approved  bool                    `json:"approved"`
}

func (r AnswerRequest) Valid() bool {
	if !boundedID(r.AccountID, 100) || !boundedID(r.JobID, 32) || !boundedID(r.PreviewID, 64) || !boundedID(r.Digest, 64) || len(r.SourceIDs) > ai.MaxAnswerSources {
		return false
	}
	base := r.Question == "" && r.Filter == (repository.SearchFilter{})
	controls := r.JobID == "" && r.PreviewID == "" && r.Digest == "" && len(r.SourceIDs) == 0 && !r.Approved
	switch r.Operation {
	case "retrieve":
		return r.AccountID != "" && controls && retrieval.ValidQuery(r.Question, 5, r.Filter)
	case "status":
		return base && r.PreviewID == "" && r.Digest == "" && len(r.SourceIDs) == 0 && !r.Approved && (r.JobID == "" || len(r.JobID) == 32)
	case "clear":
		return r.AccountID != "" && base && controls
	case "cancel":
		return r.AccountID != "" && base && len(r.JobID) == 32 && r.PreviewID == "" && r.Digest == "" && len(r.SourceIDs) == 0 && !r.Approved
	case "select":
		if r.AccountID == "" || !base || len(r.PreviewID) != 64 || r.JobID != "" || r.Digest != "" || r.Approved || len(r.SourceIDs) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range r.SourceIDs {
			if len(id) != 2 || id[0] != 'S' || id[1] < '1' || id[1] > '8' || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	case "approve":
		return r.AccountID != "" && base && len(r.PreviewID) == 64 && len(r.Digest) == 64 && r.JobID == "" && len(r.SourceIDs) == 0 && r.Approved
	}
	return false
}

type AnswerCoverage struct {
	Eligible int `json:"eligible"`
	Semantic int `json:"semantic"`
	Keyword  int `json:"keyword"`
}
type AnswerResponse struct {
	AccountID   string           `json:"accountId"`
	State       string           `json:"state"`
	JobID       string           `json:"jobId"`
	PreviewID   string           `json:"previewId"`
	Digest      string           `json:"digest"`
	ExpiresAt   int64            `json:"expiresAt"`
	Question    string           `json:"question"`
	Provider    string           `json:"provider"`
	Model       string           `json:"model"`
	External    bool             `json:"external"`
	Policy      string           `json:"policy"`
	Payload     string           `json:"payload"`
	Evidence    []ai.Evidence    `json:"evidence"`
	SelectedIDs []string         `json:"selectedIds"`
	Coverage    AnswerCoverage   `json:"coverage"`
	Mode        string           `json:"mode"`
	Warning     string           `json:"warning"`
	Exhaustive  bool             `json:"exhaustive"`
	Aggregate   bool             `json:"aggregate"`
	Result      *ai.AnswerResult `json:"result"`
	Code        string           `json:"code"`
}

func emptyAnswer(code string) AnswerResponse {
	return AnswerResponse{State: "idle", Evidence: []ai.Evidence{}, SelectedIDs: []string{}, Code: code}
}
func boundAnswer(r AnswerResponse) AnswerResponse {
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 48*1024 {
		return emptyAnswer("response_too_large")
	}
	return r
}
