package domain

import (
	"errors"
	"strings"
	"time"
)

// Local IDs and provider IDs are distinct types. Provider IDs are opaque,
// account-scoped strings, never numbers or RFC Message-ID header values.
type AccountID string
type EmailID string
type ThreadID string
type LabelID string
type CategoryID string
type ProviderMessageID string
type ProviderThreadID string
type ProviderLabelID string
type ProviderAttachmentID string

// EmailAddress preserves the display name and mailbox separately. Incoming
// malformed addresses remain representable; this type does not assert trust.
type EmailAddress struct {
	Name    string
	Address string
}

// EmailHeader preserves one header occurrence. Header order and duplicates matter.
// Values (including authentication claims) are untrusted until an adapter or
// analyzer establishes their provenance.
type EmailHeader struct {
	Name  string
	Value string
}

// AttachmentMetadata contains no attachment bytes or local filesystem path.
// Filename and ContentID are untrusted mail data. A nil SizeBytes means unknown.
type AttachmentMetadata struct {
	ProviderID ProviderAttachmentID
	PartID     string
	Filename   string
	MIMEType   string
	SizeBytes  *int64
	Inline     bool
	ContentID  string
}

type LabelKind string

const (
	LabelKindUser   LabelKind = "user"
	LabelKindSystem LabelKind = "system"
)

// EmailLabel represents a mailbox label, not a local classification. ID may be
// empty on adapter results until the persistence layer assigns a local ID.
type EmailLabel struct {
	ID         LabelID
	AccountID  AccountID
	ProviderID ProviderLabelID
	Name       string
	Kind       LabelKind
}

// EmailCategory is a local application classification. Its ID/name do not map
// automatically to provider labels or provider-specific inbox categories.
type EmailCategory struct {
	ID          CategoryID
	Name        string
	Description string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Email is normalized mail data, independent of any provider SDK or transport.
// ID is assigned by MailPilot on persistence, and may be empty in adapter results.
// ProviderThreadID may be empty if the provider does not expose conversations.
// BodyHTML nil means absent/not fetched; a pointer to "" means present but empty.
// Bodies are untrusted; carrying HTML does not authorize rendering or fetching it.
// These values are internal Go models, not public IPC DTOs or SQL records.
type Email struct {
	ContentRevision  string // Opaque provider content identity; excludes label-only changes.
	BodyState        string // Empty = unknown; complete or unavailable after normalization.
	ID               EmailID
	AccountID        AccountID
	ProviderID       ProviderMessageID
	ProviderThreadID ProviderThreadID
	Sender           EmailAddress
	Recipients       []EmailAddress
	CCRecipients     []EmailAddress
	Subject          string
	BodyText         string
	BodyHTML         *string
	ReceivedAt       time.Time
	Headers          []EmailHeader
	Labels           []EmailLabel
	Attachments      []AttachmentMetadata
	Categories       []EmailCategory
}

// HeaderValues returns all occurrences in original order, using case-insensitive
// field-name matching. It does not combine, sanitize, or establish trust in them.
func (e Email) HeaderValues(name string) []string {
	var values []string
	for _, header := range e.Headers {
		if strings.EqualFold(header.Name, name) {
			values = append(values, header.Value)
		}
	}
	return values
}

// Validate checks normalized record structure, not deliverability or safety.
// Blank subjects/bodies/recipient lists and malformed inbound senders are valid
// representations. Adapters must not discard suspicious mail for these reasons.
func (e Email) Validate() error {
	if e.BodyState != "" && e.BodyState != "complete" && e.BodyState != "unavailable" {
		return errors.New("invalid body state")
	}
	if e.AccountID == "" {
		return errors.New("email account ID is required")
	}
	if e.ProviderID == "" {
		return errors.New("email provider ID is required")
	}
	if e.ReceivedAt.IsZero() {
		return errors.New("email received timestamp is required")
	}
	for _, label := range e.Labels {
		if label.AccountID != e.AccountID {
			return errors.New("email label belongs to another account")
		}
		if label.ProviderID == "" {
			return errors.New("label provider ID is required")
		}
		if label.Kind != LabelKindUser && label.Kind != LabelKindSystem {
			return errors.New("invalid label kind")
		}
	}
	for _, attachment := range e.Attachments {
		if attachment.SizeBytes != nil && *attachment.SizeBytes < 0 {
			return errors.New("attachment size cannot be negative")
		}
	}
	return nil
}

// EmailThread groups only messages from the same account and provider thread.
// NextPageToken is opaque; an empty token marks the end of the fetched thread.
// Never assume fetching one page returns the whole conversation.
type EmailThread struct {
	ID            ThreadID
	AccountID     AccountID
	ProviderID    ProviderThreadID
	Emails        []Email
	NextPageToken string
}

func (t EmailThread) Validate() error {
	if t.AccountID == "" || t.ProviderID == "" {
		return errors.New("thread account and provider IDs are required")
	}
	seen := make(map[ProviderMessageID]bool, len(t.Emails))
	for _, email := range t.Emails {
		if err := email.Validate(); err != nil {
			return err
		}
		if email.AccountID != t.AccountID || email.ProviderThreadID != t.ProviderID {
			return errors.New("email does not belong to this thread")
		}
		if seen[email.ProviderID] {
			return errors.New("duplicate message in thread page")
		}
		seen[email.ProviderID] = true
	}
	return nil
}
