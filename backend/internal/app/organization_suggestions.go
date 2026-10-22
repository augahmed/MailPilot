package app

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

// Fetch options for one expanded email, keeping the 25-email list small even
// when a classifier suggests all 100 categories. This is a local read only.
func (s *Organization) suggestions(ctx context.Context, account domain.AccountID, id domain.EmailID) OrganizationResponse {
	result := emptyOrganization()
	result.AccountID = string(account)
	fail := func(err error) OrganizationResponse { result.Code = orgCode(err); return result }
	run, err := s.results.Get(ctx, account, id)
	if err != nil {
		return fail(err)
	}
	if run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() {
		return fail(repository.ErrConflict)
	}
	if _, ok := organizationPrediction(run, "", true); !ok || run.Semantic == nil || run.Semantic.State != "suggested" {
		return fail(repository.ErrNotFound)
	}
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		return fail(err)
	}
	names := map[domain.CategoryID]string{}
	for _, category := range categories {
		if category.Enabled {
			names[category.ID] = category.Name
		}
	}
	stored, err := s.emails.Get(ctx, account, id)
	if err != nil {
		return fail(err)
	}
	row := OrganizationRowDTO{EmailID: string(id), Sender: shortText(stored.Email.Sender.Address, 100), Subject: shortText(stored.Email.Subject, 200), CurrentLabels: []string{}, Change: "multiple_suggestions", SuggestionCount: len(run.Results), Suggestions: []OrganizationSuggestion{}}
	for _, prediction := range run.Results {
		name, ok := names[prediction.CategoryID]
		if !ok {
			return fail(repository.ErrConflict)
		}
		row.Suggestions = append(row.Suggestions, OrganizationSuggestion{CategoryID: string(prediction.CategoryID), Name: name, Confidence: prediction.Confidence})
	}
	result.Rows = []OrganizationRowDTO{row}
	return boundedOrganization(result)
}
