package app

import (
	"context"
	"encoding/json"
	"errors"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

const reviewPageSize = 5

type ClassificationRequest struct {
	EmailID    string `json:"emailId,omitempty"`
	CategoryID string `json:"categoryId,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Operation  string `json:"operation"`
	AccountID  string `json:"accountId"`
	Offset     int    `json:"offset"`
}

func (p ClassificationRequest) Valid() bool {
	if len(p.AccountID) > 100 || p.Offset < 0 || p.Offset > 1000000 || len(p.EmailID) > 100 || len(p.CategoryID) > 100 {
		return false
	}
	switch p.Operation {
	case "list", "run":
		return (p.Operation == "list" || p.AccountID != "") && p.EmailID == "" && p.CategoryID == "" && p.Revision == ""
	case "assigned", "unclassified", "auto":
		return p.AccountID != "" && p.EmailID != "" && validReviewRevision(p.Revision) && ((p.Operation == "assigned") == (p.CategoryID != ""))
	}
	return false
}

type ClassificationDTO struct {
	ScoreKind         string   `json:"score_kind,omitempty"`
	EmailID           string   `json:"email_id"`
	CategoryID        string   `json:"category_id"`
	CategoryName      string   `json:"category_name"`
	Confidence        float64  `json:"confidence"`
	ClassifierName    string   `json:"classifier_name"`
	ClassifierVersion string   `json:"classifier_version"`
	Reasons           []string `json:"reasons"`
	Timestamp         int64    `json:"timestamp"`
}
type ClassificationAssessmentDTO struct {
	UsedClassifier string     `json:"used_classifier"`
	Reason         string     `json:"reason"`
	Candidates     []struct{} `json:"candidates"`
}
type ClassificationRow struct {
	Semantic       *SemanticReviewDTO           `json:"semantic,omitempty"`
	ReaderRevision string                       `json:"readerRevision,omitempty"`
	Assessment     *ClassificationAssessmentDTO `json:"assessment,omitempty"`
	EmailID        string                       `json:"email_id"`
	Sender         string                       `json:"sender"`
	Subject        string                       `json:"subject"`
	BodyState      string                       `json:"bodyState"`
	State          string                       `json:"state"`
	Predictions    []ClassificationDTO          `json:"predictions"`
	Timestamp      *int64                       `json:"timestamp"`
	Code           string                       `json:"code,omitempty"`
}
type ClassificationPage struct {
	CategoryOptions []ReviewCategory    `json:"categoryOptions,omitempty"`
	AccountID       string              `json:"accountId"`
	Rows            []ClassificationRow `json:"rows"`
	HasMore         bool                `json:"hasMore"`
	Processed       int                 `json:"processed"`
	Errors          int                 `json:"errors"`
	Code            string              `json:"code,omitempty"`
}

type classificationEmails interface {
	List(context.Context, domain.AccountID, repository.Page) ([]repository.StoredEmail, error)
}
type classificationCategories interface {
	List(context.Context, domain.AccountID) ([]domain.EmailCategory, error)
}

// ClassificationReview owns account authorization, bounded local work and DTO
// mapping. Persistence rejects any result based on changed input snapshots.
type ClassificationReview struct {
	session    func() (domain.AccountID, context.Context, error)
	engine     classification.ClassificationEngine
	emails     classificationEmails
	categories classificationCategories
	results    repository.ClassificationRepository
	semantic   repository.SemanticRepository
	chunks     repository.ChunkRepository
}

func NewClassificationReview(session func() (domain.AccountID, context.Context, error), engine classification.ClassificationEngine, emails classificationEmails, categories classificationCategories, results repository.ClassificationRepository) *ClassificationReview {
	return &ClassificationReview{session: session, engine: engine, emails: emails, categories: categories, results: results}
}
func (a *App) ReviewClassifications(ctx context.Context, p ClassificationRequest) ClassificationPage {
	if a.Classification == nil {
		return ClassificationPage{Rows: []ClassificationRow{}, Code: "unauthorized"}
	}
	return a.Classification.Review(ctx, p)
}
func classificationCode(err error) string {
	switch {
	case errors.Is(err, repository.ErrConflict):
		return "conflict"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(err, classification.ErrInvalidInput):
		return "invalid_message"
	default:
		return "storage_error"
	}
}
func (s *ClassificationReview) Review(ctx context.Context, p ClassificationRequest) ClassificationPage {
	page := ClassificationPage{Rows: []ClassificationRow{}}
	if !p.Valid() {
		page.Code = "validation"
		return page
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || (p.AccountID != "" && p.AccountID != string(account)) {
		page.Code = "unauthorized"
		return page
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	if p.Operation != "list" && p.Operation != "run" {
		if err = s.preference(ctx, account, p); err != nil {
			page.Code = classificationCode(err)
			return page
		}
	}
	page.AccountID = string(account)
	emails, err := s.emails.List(ctx, account, repository.Page{Limit: reviewPageSize + 1, Offset: p.Offset})
	if err != nil {
		page.Code = classificationCode(err)
		return page
	}
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		page.Code = classificationCode(err)
		return page
	}
	names := map[domain.CategoryID]string{}
	for _, c := range categories {
		names[c.ID] = c.Name
		if s.semantic != nil && c.Enabled && len(page.CategoryOptions) < 100 {
			page.CategoryOptions = append(page.CategoryOptions, ReviewCategory{string(c.ID), shortText(c.Name, 100)})
		}
	}
	page.HasMore = len(emails) > reviewPageSize
	for _, stored := range emails[:min(reviewPageSize, len(emails))] {
		email := stored.Email
		sender := email.Sender.Name
		if sender == "" {
			sender = email.Sender.Address
		}
		row := ClassificationRow{EmailID: string(email.ID), Sender: shortText(sender, 100), Subject: shortText(email.Subject, 200), BodyState: email.BodyState, State: "not_reviewed", Predictions: []ClassificationDTO{}}
		if s.semantic != nil {
			row.ReaderRevision = mailtext.ReaderRevision(stored.ContentHash)
		}
		if p.Operation == "run" || (p.EmailID != "" && p.EmailID == string(email.ID)) {
			var run domain.ClassificationRun
			run, err = s.engine.Classify(ctx, email, categories)
			if err == nil && (run.AccountID != account || run.EmailID != email.ID || run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || !classification.FreshInput(run, email, categories)) {
				err = classification.ErrInvalidInput
			}
			if err == nil {
				err = s.results.Replace(ctx, run)
			}
			if err != nil {
				row.State = "error"
				row.Code = classificationCode(err)
				page.Errors++
				page.Rows = append(page.Rows, row)
				continue
			}
			page.Processed++
		}
		run, err := s.results.Get(ctx, account, email.ID)
		switch {
		case errors.Is(err, repository.ErrNotFound): // No assessment has been recorded yet.
		case errors.Is(err, repository.ErrConflict):
			row.State = "stale"
		case err != nil:
			row.State = "error"
			row.Code = classificationCode(err)
			page.Errors++
		case run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || !classification.FreshInput(run, email, categories):
			row.State = "stale"
		default:
			timestamp := run.Timestamp.UnixMilli()
			row.Timestamp = &timestamp
			row.State = "unclassified"
			if run.Reason != "" {
				row.Assessment = &ClassificationAssessmentDTO{UsedClassifier: run.UsedClassifier, Reason: run.Reason, Candidates: []struct{}{}}
			}
			if len(run.Results) > 0 {
				row.State = "classified"
			}
			if run.Semantic != nil {
				row.State = run.Semantic.State
				row.Semantic, err = s.semanticDTO(ctx, run, names)
				if err != nil {
					row.State = "error"
					row.Code = classificationCode(err)
					row.Timestamp = nil
					row.Assessment = nil
					row.Semantic = nil
					page.Errors++
					page.Rows = append(page.Rows, row)
					continue
				}
			}
			for _, result := range run.Results {
				row.Predictions = append(row.Predictions, ClassificationDTO{ScoreKind: result.ScoreKind, EmailID: string(result.EmailID), CategoryID: string(result.CategoryID), CategoryName: shortText(names[result.CategoryID], 100), Confidence: result.Confidence, ClassifierName: result.ClassifierName, ClassifierVersion: result.ClassifierVersion, Reasons: result.Reasons, Timestamp: result.Timestamp.UnixMilli()})
			}
		}
		page.Rows = append(page.Rows, row)
	}
	if session.Err() != nil {
		return ClassificationPage{Rows: []ClassificationRow{}, Code: "unauthorized"}
	}
	return boundedClassificationPage(page)
}

// Keep JSON escaping and added assessment evidence inside the private frame.
// Oversized evidence remains persisted, but its row reports a display error.
func boundedClassificationPage(page ClassificationPage) ClassificationPage {
	for i := len(page.Rows) - 1; i >= 0; i-- {
		data, err := json.Marshal(page)
		if err == nil && len(data) <= 48*1024 {
			return page
		}
		row := &page.Rows[i]
		if row.State == "classified" || row.State == "unclassified" || row.State == "suggested" || row.State == "assigned" {
			row.State = "error"
			row.Code = "invalid_message"
			row.Timestamp = nil
			row.Predictions = []ClassificationDTO{}
			row.Assessment = nil
			row.Semantic = nil
			page.Errors++
		}
	}
	return page
}
