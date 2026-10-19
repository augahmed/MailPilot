// Package provider defines account-bound, provider-neutral email adapter contracts.
// Provider SDK types belong only in the corresponding adapter package.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
)

var (
	ErrConflict               = errors.New("provider state conflict")
	ErrUnavailable            = errors.New("provider unavailable")
	ErrMalformed              = errors.New("invalid provider message")
	ErrRateLimited            = errors.New("provider rate limited")
	ErrUnsupported            = errors.New("provider operation unsupported")
	ErrNotFound               = errors.New("provider resource not found")
	ErrAuthenticationRequired = errors.New("provider authentication required")
	ErrInvalidArgument        = errors.New("invalid provider argument")
)

// Capabilities describes implemented semantics, not whether OAuth currently
// grants permission. Unsupported operations must return ErrUnsupported.
type Capabilities struct {
	Threads      bool
	CreateLabels bool
	ModifyLabels bool
	CreateDrafts bool
}

// Account contains identity only: passwords, access/refresh tokens, raw OAuth
// responses, and credentials must never appear in provider-neutral results.
type Account struct {
	ID                domain.AccountID
	Provider          string
	ProviderAccountID string
	Address           domain.EmailAddress
}

type Authenticator interface {
	// Authenticate uses the adapter's configured OAuth flow/permissions and OS
	// credential store. It takes no password. Every adapter is bound to one account.
	Authenticate(context.Context) (Account, error)
}

// PageOptions bounds a request. Tokens must be reused only for the originating
// account, method, and unchanged filter. Empty token starts pagination.
type PageOptions struct {
	Limit     int
	PageToken string
}

func (p PageOptions) Validate() error {
	if p.Limit < 1 || p.Limit > 100 {
		return fmt.Errorf("%w: page limit must be between 1 and 100", ErrInvalidArgument)
	}
	return nil
}

// ListMessagesOptions uses neutral filters, never provider query languages.
// ReceivedAfter is an inclusive lower bound; nil means no date restriction.
// LabelIDs requires all listed labels. Adapters must reject unsupported filters,
// not silently return an unfiltered mailbox. Page ordering is provider-defined.
type ListMessagesOptions struct {
	Page          PageOptions
	ReceivedAfter *time.Time
	LabelIDs      []domain.ProviderLabelID
}

func (o ListMessagesOptions) Validate() error {
	if err := o.Page.Validate(); err != nil {
		return err
	}
	if o.ReceivedAfter != nil && o.ReceivedAfter.IsZero() {
		return fmt.Errorf("%w: invalid received timestamp", ErrInvalidArgument)
	}
	for _, id := range o.LabelIDs {
		if id == "" {
			return fmt.Errorf("%w: empty label ID", ErrInvalidArgument)
		}
	}
	return nil
}

// MessageReference avoids pretending list results contain downloaded bodies.
type MessageReference struct {
	AccountID        domain.AccountID
	ProviderID       domain.ProviderMessageID
	ProviderThreadID domain.ProviderThreadID
}
type MessagePage struct {
	Messages      []MessageReference
	NextPageToken string
}
type LabelPage struct {
	Labels        []domain.EmailLabel
	NextPageToken string
}

type Reader interface {
	ListMessages(context.Context, ListMessagesOptions) (MessagePage, error)
	FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error)
	FetchThread(context.Context, domain.ProviderThreadID, PageOptions) (domain.EmailThread, error)
	ListLabels(context.Context, PageOptions) (LabelPage, error)
}

type LabelManager interface {
	// CreateLabel creates a user label. Names are display strings, not Gmail IDs.
	CreateLabel(ctx context.Context, name string) (domain.EmailLabel, error)
	// ApplyLabel/RemoveLabel change membership on one message. They must be
	// state-idempotent and may only be called by the confirmed action workflow.
	ApplyLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error
	RemoveLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error
}

// DraftInput is deliberately separate from a received Email: no forged receive
// time, remote ID, or arbitrary raw headers. Sender is the authenticated account.
// Empty recipients/body are allowed for incomplete drafts. No attachment upload
// is exposed. Reply metadata is explicit and validated, never arbitrary headers.
type DraftInput struct {
	ThreadID      domain.ProviderThreadID
	MessageID     string // Application-generated RFC Message-ID for read-only reconciliation.
	InReplyTo     string
	References    string
	Recipients    []domain.EmailAddress
	CCRecipients  []domain.EmailAddress
	BCCRecipients []domain.EmailAddress
	Subject       string
	BodyText      string
	BodyHTML      *string
}

// Validate rejects header injection in outgoing draft metadata. Incoming Email
// validation remains permissive so malformed or hostile mail can be inspected.
var messageIDPattern = regexp.MustCompile(`^<[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9.-]+>$`)

func ValidMessageID(id string) bool { return len(id) <= 250 && messageIDPattern.MatchString(id) }
func (d DraftInput) Validate() error {
	if len(d.Subject) > 512 || !utf8.ValidString(d.Subject) || strings.ContainsFunc(d.Subject, unicode.IsControl) || len(d.BodyText) > 16384 || !utf8.ValidString(d.BodyText) || strings.ContainsRune(d.BodyText, '\x00') || len(d.Recipients)+len(d.CCRecipients)+len(d.BCCRecipients) > 20 {
		return ErrInvalidArgument
	}
	if d.MessageID != "" && !ValidMessageID(d.MessageID) || d.InReplyTo != "" && !ValidMessageID(d.InReplyTo) || len(d.References) > 1500 {
		return ErrInvalidArgument
	}
	for _, id := range strings.Fields(d.References) {
		if !ValidMessageID(id) {
			return ErrInvalidArgument
		}
	}
	if strings.ContainsAny(d.References, "\r\n") || len(d.ThreadID) > 1024 || strings.ContainsAny(string(d.ThreadID), "/\\?#\r\n") || d.ThreadID != "" && (d.InReplyTo == "" || d.References == "") {
		return ErrInvalidArgument
	}

	if strings.ContainsAny(d.Subject, "\r\n") {
		return fmt.Errorf("%w: draft subject contains a line break", ErrInvalidArgument)
	}
	for _, group := range [][]domain.EmailAddress{d.Recipients, d.CCRecipients, d.BCCRecipients} {
		for _, address := range group {
			if len(address.Address) > 320 || len(address.Name) > 256 || strings.ContainsFunc(address.Name+address.Address, unicode.IsControl) {
				return fmt.Errorf("%w: draft address contains a line break", ErrInvalidArgument)
			}
			parsed, err := mail.ParseAddress(address.Address)
			if err != nil || parsed.Address != address.Address || parsed.Name != "" {
				return fmt.Errorf("%w: draft address must contain one mailbox", ErrInvalidArgument)
			}
		}
	}
	return nil
}

type DraftReference struct {
	AccountID         domain.AccountID
	ProviderDraftID   string
	ProviderMessageID domain.ProviderMessageID
}

// DraftFinder reconciles only an application-generated draft identity. No
// general provider search string, sending or deletion capability is exposed.
type DraftFinder interface {
	FindDraft(context.Context, DraftInput) (DraftReference, error)
}

// EmailProvider is the full adapter boundary. Consumers should accept only the
// smaller interface they need. IDs passed to an adapter belong to its configured
// account; adapters must check context cancellation, validate options, return
// normalized models and map errors without leaking SDK objects or raw responses.
// There is intentionally no Send, Delete, Move, or arbitrary request method.
type EmailProvider interface {
	Authenticator
	Reader
	LabelManager
	Capabilities() Capabilities
}

// MetadataReader allows cached bodies to be reused when the provider guarantees
// ContentRevision is unchanged. Empty revision always requires a full fetch.
type MessageMetadata struct {
	// InSyncScope says whether an uncached message belongs to the initial import scope.
	InSyncScope      bool
	ProviderID       domain.ProviderMessageID
	ProviderThreadID domain.ProviderThreadID
	ContentRevision  string
	Labels           []domain.EmailLabel
}
type MetadataReader interface {
	FetchMessageMetadata(context.Context, domain.ProviderMessageID) (MessageMetadata, error)
}
