// Package repository defines local persistence contracts without exposing SQL.
package repository

import (
	"context"
	"errors"
	"time"

	"mailpilot.local/core/internal/domain"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record conflict")
	ErrInvalid  = errors.New("invalid persistence input")
	ErrStorage  = errors.New("storage operation failed")
)

type Page struct {
	Limit  int
	Offset int
}

func (p Page) Validate() error {
	if p.Limit < 1 || p.Limit > 100 || p.Offset < 0 {
		return ErrInvalid
	}
	return nil
}

type StoredEmail struct {
	Email       domain.Email
	ContentHash string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AccountRepository interface {
	Upsert(context.Context, domain.Account) (domain.AccountID, error)
	Get(context.Context, domain.AccountID) (domain.Account, error)
}
type EmailRepository interface {
	ListSummaries(context.Context, domain.AccountID, Page) ([]EmailSummary, error)
	// Upsert replaces a complete normalized provider snapshot. It preserves local
	// categories (Email.Categories is read-only here); use CategoryRepository.Assign.
	// An empty ID is allocated once. Replays preserve IDs and do not duplicate children.
	Upsert(context.Context, domain.Email) (domain.EmailID, error)
	// Batch is atomic and bounded to 100 emails, all in the explicit account.
	UpsertBatch(context.Context, domain.AccountID, []domain.Email) ([]domain.EmailID, error)
	Get(context.Context, domain.AccountID, domain.EmailID) (StoredEmail, error)
	GetByProviderID(context.Context, domain.AccountID, domain.ProviderMessageID) (StoredEmail, error)
	List(context.Context, domain.AccountID, Page) ([]StoredEmail, error)
	SavePhishingResult(context.Context, domain.PhishingResult) error
	GetPhishingResult(context.Context, domain.AccountID, domain.EmailID) (domain.PhishingResult, error)
}
type CategoryRepository interface {
	Delete(context.Context, domain.AccountID, domain.CategoryID) error
	Save(context.Context, domain.AccountID, domain.EmailCategory) (domain.CategoryID, error)
	Get(context.Context, domain.AccountID, domain.CategoryID) (domain.EmailCategory, error)
	List(context.Context, domain.AccountID) ([]domain.EmailCategory, error)
	Assign(context.Context, domain.AccountID, domain.EmailID, domain.CategoryID) error
	Unassign(context.Context, domain.AccountID, domain.EmailID, domain.CategoryID) error
}

// ClassificationRepository atomically replaces a complete assessment, including
// an empty prediction set, only while its email/category fingerprint is current.
type ClassificationRepository interface {
	Replace(context.Context, domain.ClassificationRun) error
	Get(context.Context, domain.AccountID, domain.EmailID) (domain.ClassificationRun, error)
}

type ActionItemUpdate struct {
	EmailID       domain.EmailID
	State         domain.ActionItemState
	ActualDelta   *domain.LabelDelta
	AfterLabels   *[]domain.ProviderLabelID
	LastErrorCode string
}
type MailboxActionRepository interface {
	// Create stores an immutable preview and snapshots. IDs are allocated if absent;
	// an existing action ID is a conflict, never an overwrite or a remote retry.
	Create(context.Context, domain.MailboxAction) (domain.ActionID, error)
	Get(context.Context, domain.AccountID, domain.ActionID) (domain.MailboxAction, error)
	ListRecoverable(context.Context, domain.AccountID, Page) ([]domain.MailboxAction, error)
	// Revision checks prevent competing workers from overwriting outcomes.
	Transition(context.Context, domain.AccountID, domain.ActionID, int64, domain.ActionStatus) error
	UpdateItem(context.Context, domain.AccountID, domain.ActionID, int64, ActionItemUpdate) error
}

type EmailSummary struct {
	ID         domain.EmailID
	Sender     domain.EmailAddress
	Subject    string
	ReceivedAt time.Time
	BodyState  string
}

// OrganizationRepository extends the action journal with label-creation evidence.
// Update permits only outcome transitions; the confirmed plan remains immutable.
type OrganizationRepository interface {
	Create(context.Context, domain.OrganizationPlan) (domain.OrganizationPlan, error)
	Get(context.Context, domain.AccountID, domain.ActionID) (domain.OrganizationPlan, error)
	Latest(context.Context, domain.AccountID, bool) (domain.OrganizationPlan, error)
	Update(context.Context, domain.OrganizationPlan) (domain.OrganizationPlan, error)
}
