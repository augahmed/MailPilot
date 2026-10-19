package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type ReplyRequest struct {
	Operation string `json:"operation"`
	AccountID string `json:"accountId"`
	JobID     string `json:"jobId"`
	ActionID  string `json:"actionId"`
	Approved  bool   `json:"approved"`
}

func (p ReplyRequest) Valid() bool {
	for _, v := range []string{p.AccountID, p.JobID, p.ActionID} {
		if len(v) > 100 || !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	switch p.Operation {
	case "status":
		return p.JobID == "" && p.ActionID == "" && !p.Approved
	case "reconcile":
		return p.AccountID != "" && p.ActionID != "" && p.JobID == "" && !p.Approved
	case "resolve":
		return p.AccountID != "" && p.ActionID != "" && p.JobID == "" && p.Approved
	case "cancel":
		return p.AccountID != "" && p.JobID != "" && p.ActionID == "" && !p.Approved
	default:
		return false
	}
}

type ReplyResponse struct {
	AccountID  string `json:"accountId"`
	State      string `json:"state"`
	JobID      string `json:"jobId"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	ActionID   string `json:"actionId"`
	DraftID    string `json:"draftId"`
	Reconciled bool   `json:"reconciled"`
	Code       string `json:"code"`
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
