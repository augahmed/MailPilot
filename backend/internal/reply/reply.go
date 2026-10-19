// Package reply preserves historical draft journal values and read-only recovery.
package reply

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

const MaxBody = 6000

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
