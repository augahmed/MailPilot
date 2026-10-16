package app

import (
	"context"
	"encoding/json"
	"errors"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

const reviewPageSize = 5

type ClassificationRequest struct {
	Operation string `json:"operation"`
	AccountID string `json:"accountId"`
	Offset    int    `json:"offset"`
}

func (p ClassificationRequest) Valid() bool {
	return len(p.AccountID) <= 100 && p.Offset >= 0 && p.Offset <= 1000000 && (p.Operation == "list" || (p.Operation == "run" && p.AccountID != ""))
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
	Assessment  *ClassificationAssessmentDTO `json:"assessment,omitempty"`
	EmailID     string                       `json:"email_id"`
	Sender      string                       `json:"sender"`
	Subject     string                       `json:"subject"`
	BodyState   string                       `json:"bodyState"`
	State       string                       `json:"state"`
	Predictions []ClassificationDTO          `json:"predictions"`
	Timestamp   *int64                       `json:"timestamp"`
	Code        string                       `json:"code,omitempty"`
}
type ClassificationPage struct {
	AccountID string              `json:"accountId"`
	Rows      []ClassificationRow `json:"rows"`
	HasMore   bool                `json:"hasMore"`
	Processed int                 `json:"processed"`
	Errors    int                 `json:"errors"`
	Code      string              `json:"code,omitempty"`
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
}

func NewClassificationReview(session func() (domain.AccountID, context.Context, error), engine classification.ClassificationEngine, emails classificationEmails, categories classificationCategories, results repository.ClassificationRepository) *ClassificationReview {
	return &ClassificationReview{session, engine, emails, categories, results}
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
	}
	page.HasMore = len(emails) > reviewPageSize
	for _, stored := range emails[:min(reviewPageSize, len(emails))] {
		email := stored.Email
		sender := email.Sender.Name
		if sender == "" {
			sender = email.Sender.Address
		}
		row := ClassificationRow{EmailID: string(email.ID), Sender: shortText(sender, 100), Subject: shortText(email.Subject, 200), BodyState: email.BodyState, State: "not_reviewed", Predictions: []ClassificationDTO{}}
		if p.Operation == "run" {
			var run domain.ClassificationRun
			run, err = s.engine.Classify(ctx, email, categories)
			if err == nil && (run.AccountID != account || run.EmailID != email.ID || run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || run.InputHash != classification.InputHash(email, categories)) {
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
		case run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || run.InputHash != classification.InputHash(email, categories):
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
		if row.State == "classified" || row.State == "unclassified" {
			row.State = "error"
			row.Code = "invalid_message"
			row.Timestamp = nil
			row.Predictions = []ClassificationDTO{}
			row.Assessment = nil
			page.Errors++
		}
	}
	return page
}
