package app

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"reflect"
)

var errRiskBelowThreshold = errors.New("risk below configured threshold")

// proposal supplies evidence to the existing label planning pipeline. The source
// selects suggestions only; apply and undo use the immutable journaled evidence.
func (s *Organization) proposal(ctx context.Context, account domain.AccountID, id domain.EmailID, source string) (domain.OrganizationRow, domain.Email, error) {
	if source == "phishing" {
		p, err := assessRisk(ctx, s.emails, s.riskEngine, account, id)
		if err != nil {
			return domain.OrganizationRow{}, domain.Email{}, err
		}
		if s.riskEngine.Level(p.Score) != "HIGH" {
			return domain.OrganizationRow{}, domain.Email{}, errRiskBelowThreshold
		}
		stored, err := s.emails.Get(ctx, account, id)
		if err != nil {
			return domain.OrganizationRow{}, domain.Email{}, err
		}
		if stored.ContentHash != p.InputHash {
			return domain.OrganizationRow{}, domain.Email{}, repository.ErrConflict
		}
		row := domain.OrganizationRow{InputHash: p.InputHash, ProposedLabel: domain.PotentialPhishingLabel, Phishing: &domain.OrganizationRisk{Score: p.Score, Threshold: s.riskEngine.HighThreshold(), Version: p.AnalyzerVersion, Findings: p.Findings}}
		return row, stored.Email, nil
	}
	run, cat, email, err := s.prediction(ctx, account, id)
	if err != nil {
		return domain.OrganizationRow{}, email, err
	}
	row := domain.OrganizationRow{CategoryID: cat.ID, InputHash: run.InputHash, PredictionTimestamp: run.Timestamp.UnixMilli(), ProposedLabel: "MailPilot/" + cat.Name, Confidence: run.Results[0].Confidence}
	if run.Semantic != nil {
		row.Classification = &domain.OrganizationClassification{Version: "semantic-organization-v1", AssessmentHash: run.InputHash, BaseHash: run.Semantic.BaseHash, SourceRevision: run.Semantic.SourceRevision, ModelVersion: run.Semantic.Profile.ModelVersion, ClassifierVersion: run.ClassifierVersion, ScoreKind: run.Results[0].ScoreKind}
	}
	return row, email, nil
}
func (s *Organization) checkProposal(ctx context.Context, account domain.AccountID, row domain.OrganizationRow) error {
	source := ""
	if row.Phishing != nil {
		source = "phishing"
	}
	current, email, err := s.proposal(ctx, account, row.EmailID, source)
	if err != nil {
		return err
	}
	if email.ProviderID != row.ProviderID || current.InputHash != row.InputHash || current.PredictionTimestamp != row.PredictionTimestamp || current.CategoryID != row.CategoryID || current.ProposedLabel != row.ProposedLabel || !reflect.DeepEqual(current.Phishing, row.Phishing) || !reflect.DeepEqual(current.Classification, row.Classification) || current.Confidence != row.Confidence {
		return repository.ErrConflict
	}
	return nil
}
func matchesOrganizationInput(email domain.Email, categories []domain.EmailCategory, row domain.OrganizationRow) bool {
	if row.Phishing != nil {
		hash, err := repository.EmailContentHash(email)
		return err == nil && hash == row.InputHash
	}
	if row.Classification != nil {
		evidence := row.Classification
		hash, err := repository.EmailContentHash(email)
		return evidence.Version == "semantic-organization-v1" && evidence.AssessmentHash == row.InputHash && classification.InputHash(email, categories) == evidence.BaseHash && err == nil && hash == evidence.SourceRevision
	}
	return classification.InputHash(email, categories) == row.InputHash
}
