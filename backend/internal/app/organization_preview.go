package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

func (s *Organization) list(ctx context.Context, account domain.AccountID, offset int, source string) OrganizationResponse {
	r := emptyOrganization()
	r.AccountID = string(account)
	emails, err := s.emails.List(ctx, account, repository.Page{Limit: 6, Offset: offset})
	if err != nil {
		r.Code = orgCode(err)
		return r
	}
	r.HasMore = len(emails) > 5
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		r.Code = orgCode(err)
		return r
	}
	names := map[domain.CategoryID]string{}
	for _, c := range categories {
		if c.Enabled {
			names[c.ID] = c.Name
		}
	}
	for _, stored := range emails[:min(5, len(emails))] {
		email := stored.Email
		row := OrganizationRowDTO{EmailID: string(email.ID), Sender: shortText(email.Sender.Address, 100), Subject: shortText(email.Subject, 200), CurrentLabels: []string{}, Change: "unclassified"}
		for _, label := range email.Labels[:min(len(email.Labels), 50)] {
			row.CurrentLabels = append(row.CurrentLabels, shortText(label.Name, 100))
		}
		if source == "phishing" {
			proposal, _, err := s.proposal(ctx, account, email.ID, source)
			if err == nil {
				row.ProposedLabel = proposal.ProposedLabel
				row.Phishing = organizationRiskDTO(proposal.Phishing)
				row.Change = "candidate"
			} else if !errors.Is(err, errRiskBelowThreshold) {
				row.Change = "risk_unavailable"
			} else {
				row.Change = "below_threshold"
			}
			r.Rows = append(r.Rows, row)
			continue
		}
		run, err := s.results.Get(ctx, account, email.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) && !errors.Is(err, repository.ErrConflict) {
			r.Code = orgCode(err)
			return boundedOrganization(r)
		}
		if err == nil && run.ClassifierName == s.engine.Name() && run.ClassifierVersion == s.engine.Version() && len(run.Results) == 1 {
			prediction := run.Results[0]
			name, ok := names[prediction.CategoryID]
			if ok {
				row.ProposedLabel = "MailPilot/" + name
				row.Confidence = prediction.Confidence
				row.ScoreKind = prediction.ScoreKind
				row.Change = "candidate"
			}
		}
		if err == nil && run.ClassifierName == s.engine.Name() && run.ClassifierVersion == s.engine.Version() && len(run.Results) > 1 {
			row.Change = "multiple_suggestions"
		}
		r.Rows = append(r.Rows, row)
	}
	return boundedOrganization(r)
}
func labelsSnapshot(account domain.AccountID, labels []domain.EmailLabel) ([]domain.ProviderLabelID, []string, error) {
	if len(labels) > 50 {
		return nil, nil, repository.ErrInvalid
	}
	ids := []domain.ProviderLabelID{}
	names := []string{}
	seen := map[domain.ProviderLabelID]bool{}
	for _, l := range labels {
		if l.AccountID != account || l.ProviderID == "" || seen[l.ProviderID] || len(l.Name) > 200 {
			return nil, nil, provider.ErrMalformed
		}
		seen[l.ProviderID] = true
		ids = append(ids, l.ProviderID)
		names = append(names, l.Name)
	}
	slices.Sort(ids)
	slices.Sort(names)
	return ids, names, nil
}
func listOrganizationLabels(ctx context.Context, account domain.AccountID, p OrganizationProvider) (map[string]domain.EmailLabel, error) {
	labels := map[string]domain.EmailLabel{}
	tokens := map[string]bool{}
	token := ""
	for page := 0; page < 100; page++ {
		if tokens[token] {
			return nil, provider.ErrMalformed
		}
		tokens[token] = true
		result, err := p.ListLabels(ctx, provider.PageOptions{Limit: 100, PageToken: token})
		if err != nil {
			return nil, err
		}
		if len(result.Labels) > 100 {
			return nil, provider.ErrMalformed
		}
		for _, l := range result.Labels {
			if l.AccountID != account || l.ProviderID == "" {
				return nil, provider.ErrMalformed
			}
			if _, exists := labels[l.Name]; exists {
				return nil, repository.ErrConflict
			}
			labels[l.Name] = l
		}
		if result.NextPageToken == "" {
			return labels, nil
		}
		token = result.NextPageToken
	}
	return nil, repository.ErrInvalid
}
func (s *Organization) prediction(ctx context.Context, account domain.AccountID, id domain.EmailID) (domain.ClassificationRun, domain.EmailCategory, domain.Email, error) {
	run, err := s.results.Get(ctx, account, id)
	if err != nil {
		return run, domain.EmailCategory{}, domain.Email{}, err
	}
	if run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || len(run.Results) != 1 {
		return run, domain.EmailCategory{}, domain.Email{}, repository.ErrConflict
	}
	cat, err := s.categories.Get(ctx, account, run.Results[0].CategoryID)
	if err != nil {
		return run, cat, domain.Email{}, err
	}
	if !cat.Enabled || strings.ContainsFunc(cat.Name, unicode.IsControl) {
		return run, cat, domain.Email{}, repository.ErrInvalid
	}
	email, err := s.emails.Get(ctx, account, id)
	return run, cat, email.Email, err
}
func (s *Organization) preview(ctx context.Context, account domain.AccountID, req OrganizationRequest, p OrganizationProvider) (OrganizationResponse, error) {
	labels, err := listOrganizationLabels(ctx, account, p)
	if err != nil {
		return emptyOrganization(), err
	}
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		return emptyOrganization(), err
	}
	plan := domain.OrganizationPlan{AccountID: account, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{}, Steps: []domain.OrganizationStep{}}
	creates := map[string]bool{}
	for _, id := range req.EmailIDs {
		row, email, err := s.proposal(ctx, account, domain.EmailID(id), req.Source)
		if err != nil {
			return emptyOrganization(), err
		}
		live, err := p.FetchMessage(ctx, email.ProviderID)
		if err != nil {
			return emptyOrganization(), err
		}
		if live.AccountID != account || live.ProviderID != email.ProviderID || !matchesOrganizationInput(live, categories, row) {
			return emptyOrganization(), repository.ErrConflict
		}
		before, names, err := labelsSnapshot(account, live.Labels)
		if err != nil {
			return emptyOrganization(), err
		}
		name := row.ProposedLabel
		row.EmailID = email.ID
		row.ProviderID = email.ProviderID
		row.Sender = shortText(email.Sender.Address, 100)
		row.Subject = shortText(email.Subject, 200)
		row.CurrentLabels = names
		row.BeforeLabels = before
		row.Change = "add"
		label, exists := labels[name]
		if exists {
			if label.Kind != domain.LabelKindUser {
				return emptyOrganization(), repository.ErrConflict
			}
			row.LabelID = label.ProviderID
			if slices.Contains(before, label.ProviderID) {
				row.Change = "none"
			}
		}
		if !exists && !creates[name] {
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "create_label", Name: name, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
			creates[name] = true
		}
		plan.Rows = append(plan.Rows, row)
	}
	// Creation steps precede all memberships and shared categories create once.
	for _, row := range plan.Rows {
		if row.Change == "add" {
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "add", Name: row.ProposedLabel, EmailID: row.EmailID, LabelID: row.LabelID, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
		}
	}
	if organizationDTO(plan).Code != "" {
		return emptyOrganization(), repository.ErrInvalid
	}
	if req.Operation == "dry_run" {
		r := organizationDTO(plan)
		r.State = "dry_run"
		r.DryRun = true
		return r, nil
	}
	saved, err := s.journal.Create(ctx, plan)
	return organizationDTO(saved), err
}
func (s *Organization) undoPreview(ctx context.Context, account domain.AccountID, p OrganizationProvider) (OrganizationResponse, error) {
	original, err := s.journal.Latest(ctx, account, true)
	if err != nil {
		return emptyOrganization(), err
	}
	plan := domain.OrganizationPlan{AccountID: account, Kind: domain.ActionUndo, Reverses: original.ID, Rows: []domain.OrganizationRow{}, Steps: []domain.OrganizationStep{}}
	for _, step := range original.Steps {
		if step.Kind != "add" || step.State != domain.ItemApplied || !step.Added {
			continue
		}
		row := findOrganizationRow(original, step.EmailID)
		live, err := p.FetchMessageMetadata(ctx, row.ProviderID)
		if err != nil {
			return emptyOrganization(), err
		}
		if live.ProviderID != row.ProviderID {
			return emptyOrganization(), provider.ErrMalformed
		}
		ids, names, err := labelsSnapshot(account, live.Labels)
		if err != nil {
			return emptyOrganization(), err
		}
		row.BeforeLabels = ids
		row.CurrentLabels = names
		row.LabelID = step.ResultID
		row.Change = "none"
		if slices.Contains(ids, step.ResultID) {
			row.Change = "remove"
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "remove", EmailID: row.EmailID, Name: row.ProposedLabel, LabelID: step.ResultID, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
		}
		plan.Rows = append(plan.Rows, row)
	}
	if len(plan.Rows) == 0 {
		return emptyOrganization(), repository.ErrNotFound
	}
	saved, err := s.journal.Create(ctx, plan)
	return organizationDTO(saved), err
}
func findOrganizationRow(p domain.OrganizationPlan, id domain.EmailID) domain.OrganizationRow {
	for _, row := range p.Rows {
		if row.EmailID == id {
			return row
		}
	}
	return domain.OrganizationRow{}
}
