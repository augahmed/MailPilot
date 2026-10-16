// Package reply owns bounded reply context and the durable draft-creation plan.
package reply

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

const MaxBody = 6000

func ToneValid(t string) bool {
	return t == "Professional" || t == "Friendly" || t == "Concise" || t == "Formal"
}

type Source struct {
	ID   domain.EmailID
	Hash string
}
type Plan struct {
	ID        string
	AccountID domain.AccountID
	EmailID   domain.EmailID
	Sources   []Source
	Draft     provider.DraftInput
	Digest    string
	ExpiresAt time.Time
}
type Action struct {
	Plan       Plan
	State      string
	Reference  provider.DraftReference
	Code       string
	Reconciled bool
}
type Store interface {
	Sources(context.Context, domain.AccountID, domain.EmailID, bool) ([]repository.StoredEmail, bool, error)
	Create(context.Context, Plan) error
	Get(context.Context, domain.AccountID, string) (Action, error)
	Latest(context.Context, domain.AccountID) (Action, error)
	Transition(context.Context, domain.AccountID, string, string, string, provider.DraftReference, string, bool) error
}

func (p Plan) Hash() string {
	p.Digest = ""
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (p Plan) Valid() bool {
	if p.ID == "" || len(p.ID) > 100 || p.AccountID == "" || p.EmailID == "" || len(p.Sources) < 1 || len(p.Sources) > 5 || p.Digest != p.Hash() || p.Draft.Validate() != nil || p.ExpiresAt.IsZero() || len(p.Draft.BodyText) > MaxBody || strings.TrimSpace(p.Draft.BodyText) == "" || len(p.Draft.Recipients) != 1 || p.Draft.ThreadID == "" || len(p.Draft.CCRecipients) != 0 || len(p.Draft.BCCRecipients) != 0 || p.Draft.BodyHTML != nil || !provider.ValidMessageID(p.Draft.InReplyTo) || p.Draft.References == "" || p.Draft.MessageID != "<mailpilot-"+p.ID+"@mailpilot.invalid>" {
		return false
	}
	seen := map[domain.EmailID]bool{}
	found := false
	for _, src := range p.Sources {
		if src.ID == "" || len(src.Hash) != 64 || seen[src.ID] {
			return false
		}
		seen[src.ID] = true
		found = found || src.ID == p.EmailID
	}
	return found
}
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Build uses selected cached messages only; no retrieval or provider is involved.
// Quoted chains/signatures are removed by the deterministic local preprocessor.
func Build(rows []repository.StoredEmail, selected domain.EmailID, tone, instruction string) (ai.Request, []Source, provider.DraftInput, bool, error) {
	if !ToneValid(tone) || len(instruction) > 1000 || !utf8.ValidString(instruction) || len(rows) == 0 || len(rows) > 5 {
		return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
	}
	var parts []string
	var sources []Source
	var target *domain.Email
	truncated := false
	account := rows[0].Email.AccountID
	thread := rows[0].Email.ProviderThreadID
	for _, row := range rows {
		e := row.Email
		if e.AccountID != account || e.ProviderThreadID != thread || e.BodyState != "complete" {
			return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
		}
		if e.ID == selected {
			copy := e
			target = &copy
		}
		prepared, err := embedding.Prepare(e)
		if err != nil {
			return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
		}
		limit := 1000
		if e.ID == selected {
			limit = 1800
		}
		truncated = truncated || len(prepared.Text) > limit
		parts = append(parts, e.ReceivedAt.UTC().Format(time.RFC3339)+"\n"+Clip(prepared.Text, limit))
		sources = append(sources, Source{e.ID, row.ContentHash})
	}
	if target == nil {
		return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
	}
	address := target.Sender.Address
	replies := target.HeaderValues("Reply-To")
	if len(replies) > 1 {
		return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
	}
	if len(replies) == 1 {
		parsed, err := mail.ParseAddress(replies[0])
		if err != nil {
			return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
		}
		address = parsed.Address
	}
	subject := target.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	ids := target.HeaderValues("Message-ID")
	if len(ids) != 1 || !provider.ValidMessageID(strings.TrimSpace(ids[0])) || thread == "" {
		return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
	}
	parent := strings.TrimSpace(ids[0])
	draft := provider.DraftInput{Recipients: []domain.EmailAddress{{Address: address}}, Subject: subject, ThreadID: thread, InReplyTo: parent, References: parent}
	if draft.Validate() != nil {
		return ai.Request{}, nil, provider.DraftInput{}, false, repository.ErrInvalid
	}
	text := "Write only the reply body for human review. Tone: " + tone + ". Reply to the selected message (the last message below). Use only this context. Do not repeat quoted history or invent commitments."
	if strings.TrimSpace(instruction) != "" {
		text += "\nUser instruction: " + strings.TrimSpace(instruction)
	}
	payload := ai.Request{Instruction: ai.NewText(text), Context: ai.NewText(strings.Join(parts, "\n\n")), MaxOutputTokens: 1200}
	return payload, sources, draft, truncated, payload.Validate()
}
