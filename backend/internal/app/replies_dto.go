package app

import (
	"encoding/json"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/reply"
	"strings"
	"unicode/utf8"
)

// KeyInput only permits incoming secret material; inherited formatting and JSON
// output are redacted. It is never included in any response or persistent plan.
type KeyInput struct{ credentials.Secret }

func (k *KeyInput) UnmarshalJSON(data []byte) error {
	var v string
	if json.Unmarshal(data, &v) != nil || len(v) > 2048 {
		return errReplyInput
	}
	k.Secret = credentials.NewSecret(v)
	return nil
}

type ReplyRequest struct {
	Operation     string   `json:"operation"`
	AccountID     string   `json:"accountId"`
	EmailID       string   `json:"emailId"`
	IncludeThread bool     `json:"includeThread"`
	Instruction   string   `json:"instruction"`
	Tone          string   `json:"tone"`
	PreviewID     string   `json:"previewId"`
	JobID         string   `json:"jobId"`
	ActionID      string   `json:"actionId"`
	Approved      bool     `json:"approved"`
	Body          string   `json:"body"`
	Model         string   `json:"model"`
	APIKey        KeyInput `json:"apiKey"`
}

func (p ReplyRequest) Valid() bool {
	for _, v := range []string{p.AccountID, p.EmailID, p.PreviewID, p.JobID, p.ActionID} {
		if len(v) > 100 || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	if len(p.Instruction) > 1000 || len(p.Body) > reply.MaxBody || len(p.Model) > 128 || !utf8.ValidString(p.Instruction+p.Body+p.Model) || strings.ContainsRune(p.Instruction+p.Body+p.Model, '\x00') {
		return false
	}
	selection := p.EmailID == "" && !p.IncludeThread && p.Instruction == "" && p.Tone == ""
	control := p.PreviewID == "" && p.JobID == "" && p.ActionID == "" && !p.Approved && p.Body == ""
	config := p.Model == "" && p.APIKey.Empty()
	if p.Operation == "status" {
		return selection && control && config
	}
	if p.AccountID == "" {
		return false
	}
	switch p.Operation {
	case "configure":
		return selection && control && p.Model != ""
	case "disable":
		return selection && control && config
	case "prepare":
		return p.EmailID != "" && reply.ToneValid(p.Tone) && control && config
	case "generate":
		return selection && config && p.PreviewID != "" && p.Approved && p.JobID == "" && p.ActionID == "" && p.Body == ""
	case "preview_draft":
		return selection && config && p.PreviewID != "" && !p.Approved && p.JobID == "" && p.ActionID == "" && strings.TrimSpace(p.Body) != ""
	case "create":
		return selection && config && p.PreviewID != "" && p.ActionID != "" && p.Approved && p.JobID == "" && p.Body == ""
	case "reconcile", "undo_preview":
		return selection && config && p.ActionID != "" && p.PreviewID == "" && p.JobID == "" && !p.Approved && p.Body == ""
	case "resolve":
		return selection && config && p.ActionID != "" && p.PreviewID == "" && p.JobID == "" && p.Approved && p.Body == ""
	case "cancel":
		return selection && config && p.JobID != "" && p.PreviewID == "" && p.ActionID == "" && !p.Approved && p.Body == ""
	default:
		return false
	}
}

type ReplyResponse struct {
	AccountID     string `json:"accountId"`
	State         string `json:"state"`
	Configured    bool   `json:"configured"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	External      bool   `json:"external"`
	JobID         string `json:"jobId"`
	PreviewID     string `json:"previewId"`
	ReviewID      string `json:"reviewId"`
	ExpiresAt     int64  `json:"expiresAt"`
	Context       string `json:"context"`
	Instruction   string `json:"instruction"`
	Policy        string `json:"policy"`
	To            string `json:"to"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	SourceCount   int    `json:"sourceCount"`
	Truncated     bool   `json:"truncated"`
	ActionID      string `json:"actionId"`
	DraftID       string `json:"draftId"`
	Reconciled    bool   `json:"reconciled"`
	UndoAvailable bool   `json:"undoAvailable"`
	Code          string `json:"code"`
}

func emptyReply() ReplyResponse { return ReplyResponse{State: "idle"} }
func boundReply(r ReplyResponse) ReplyResponse {
	b, e := json.Marshal(r)
	if e != nil || len(b) > 48*1024 {
		x := emptyReply()
		x.Code = "response_too_large"
		return x
	}
	return r
}
