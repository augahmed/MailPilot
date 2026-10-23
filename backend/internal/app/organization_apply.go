package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

// Durability must outlive cancellation after dispatch. These bounded DB-only
// writes do not authorize further network activity or a disconnected account.
func (s *Organization) save(p domain.OrganizationPlan) (domain.OrganizationPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.journal.Update(ctx, p)
}
func (s *Organization) finish(p domain.OrganizationPlan) (OrganizationResponse, error) {
	unresolved, applied, failed := false, 0, 0
	cancelled := false
	for _, step := range p.Steps {
		switch step.State {
		case domain.ItemInFlight, domain.ItemUncertain:
			unresolved = true
		case domain.ItemApplied:
			applied++
		case domain.ItemSkipped:
			if step.Code == "cancelled" || step.Code == "recovered_pending" {
				cancelled = true
			}
		default:
			failed++
		}
	}
	if unresolved {
		p.Status = domain.ActionUncertain
	} else if cancelled {
		p.Status = domain.ActionCancelled
	} else if failed == 0 {
		p.Status = domain.ActionApplied
	} else if applied > 0 {
		p.Status = domain.ActionPartial
	} else {
		p.Status = domain.ActionFailed
	}
	p, err := s.save(p)
	return organizationDTO(p), err
}
func (s *Organization) apply(ctx context.Context, plan domain.OrganizationPlan, p OrganizationProvider) (OrganizationResponse, error) {
	// Revalidate the complete plan before creating any label or dispatching writes.
	if plan.Kind == domain.ActionUndo {
		latest, err := s.journal.Latest(ctx, plan.AccountID, true)
		if err != nil {
			return organizationDTO(plan), err
		}
		if latest.ID != plan.Reverses {
			return organizationDTO(plan), repository.ErrConflict
		}
	}
	labels, err := listOrganizationLabels(ctx, plan.AccountID, p)
	if err != nil {
		return organizationDTO(plan), err
	}
	categories, err := s.categories.List(ctx, plan.AccountID)
	if err != nil {
		return organizationDTO(plan), err
	}
	for _, row := range plan.Rows {
		if plan.Kind == domain.ActionLabelDelta {
			if err := s.checkProposal(ctx, plan.AccountID, row); err != nil {
				if errors.Is(err, repository.ErrConflict) {
					err = organizationConflict("classification_changed")
				}
				return organizationDTO(plan), err
			}
			live, err := p.FetchMessage(ctx, row.ProviderID)
			if err != nil {
				return organizationDTO(plan), err
			}
			if live.AccountID != plan.AccountID || live.ProviderID != row.ProviderID || live.ID != "" && live.ID != row.EmailID {
				return organizationDTO(plan), organizationConflict("mail_changed")
			}
			ids, _, err := labelsSnapshot(plan.AccountID, live.Labels)
			if err != nil {
				return organizationDTO(plan), err
			}
			if !slices.Equal(ids, row.BeforeLabels) {
				s.cacheRejectedSnapshot(ctx, plan.AccountID, row, live)
				return organizationDTO(plan), organizationConflict("labels_changed")
			}
			if !matchesOrganizationInput(live, categories, row) {
				s.cacheRejectedSnapshot(ctx, plan.AccountID, row, live)
				return organizationDTO(plan), organizationConflict("mail_changed")
			}
		}
		l, exists := labels[row.ProposedLabel]
		if row.LabelID != "" {
			if !exists || l.Kind != domain.LabelKindUser || l.ProviderID != row.LabelID {
				return organizationDTO(plan), organizationConflict("target_label_changed")
			}
		} else if exists {
			return organizationDTO(plan), organizationConflict("target_label_changed")
		}
	}
	if ctx.Err() != nil {
		return organizationDTO(plan), ctx.Err()
	}
	plan.Status = domain.ActionApplying
	plan, err = s.save(plan)
	if err != nil {
		return organizationDTO(plan), err
	}
	resolved := map[string]domain.ProviderLabelID{}
	for _, row := range plan.Rows {
		if row.LabelID != "" {
			resolved[row.ProposedLabel] = row.LabelID
		}
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if ctx.Err() != nil {
			step.State = domain.ItemSkipped
			step.Code = "cancelled"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		if step.Kind != "create_label" && resolved[step.Name] == "" {
			step.State = domain.ItemConflict
			step.Code = "label_unresolved"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		var before []domain.ProviderLabelID
		if step.Kind != "create_label" {
			row := findOrganizationRow(plan, step.EmailID)
			if plan.Kind == domain.ActionLabelDelta {
				checkErr := s.checkProposal(ctx, plan.AccountID, row)
				if checkErr != nil {
					step.State = domain.ItemConflict
					step.Code = "prediction_changed"
					plan, err = s.save(plan)
					if err != nil {
						return organizationDTO(plan), err
					}
					continue
				}
			}
			live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
			if readErr == nil && plan.Kind == domain.ActionLabelDelta {
				full, fullErr := p.FetchMessage(ctx, row.ProviderID)
				if fullErr != nil {
					readErr = fullErr
				} else if full.AccountID != plan.AccountID || full.ProviderID != row.ProviderID || !matchesOrganizationInput(full, categories, row) {
					readErr = repository.ErrConflict
				} else {
					live.Labels = full.Labels
				}
			}
			if readErr == nil && live.ProviderID != row.ProviderID {
				readErr = provider.ErrMalformed
			}
			if readErr == nil {
				before, _, readErr = labelsSnapshot(plan.AccountID, live.Labels)
			}
			if readErr != nil {
				step.State = domain.ItemConflict
				step.Code = "live_read_failed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
			target := resolved[step.Name]
			present := slices.Contains(before, target)
			// A target changed since preview is a no-op, never owned by MailPilot.
			if step.Kind == "add" && present || step.Kind == "remove" && !present {
				step.State = domain.ItemSkipped
				step.Code = "membership_changed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
			if plan.Kind == domain.ActionLabelDelta && !slices.Equal(before, row.BeforeLabels) {
				step.State = domain.ItemConflict
				step.Code = "labels_changed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
		}
		step.State = domain.ItemInFlight
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
		step = &plan.Steps[i]
		if step.Kind == "create_label" {
			label, writeErr := p.CreateLabel(ctx, step.Name)
			if writeErr == nil && (label.AccountID != plan.AccountID || label.Kind != domain.LabelKindUser || label.Name != step.Name || label.ProviderID == "") {
				writeErr = provider.ErrMalformed
			}
			if writeErr != nil {
				step.State = domain.ItemUncertain
				step.Code = "creation_uncertain"
			} else {
				step.State = domain.ItemApplied
				step.ResultID = label.ProviderID
				resolved[step.Name] = label.ProviderID
			}
		} else {
			row := findOrganizationRow(plan, step.EmailID)
			target := resolved[step.Name]
			var writeErr error
			if step.Kind == "add" {
				writeErr = p.ApplyLabel(ctx, row.ProviderID, target)
			} else {
				writeErr = p.RemoveLabel(ctx, row.ProviderID, target)
			}
			if writeErr != nil {
				step.State = domain.ItemUncertain
				step.Code = "write_uncertain"
			} else {
				live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
				if readErr == nil && live.ProviderID != row.ProviderID {
					readErr = provider.ErrMalformed
				}
				var after []domain.ProviderLabelID
				if readErr == nil {
					after, _, readErr = labelsSnapshot(plan.AccountID, live.Labels)
				}
				if readErr != nil {
					step.State = domain.ItemUncertain
					step.Code = "verification_uncertain"
				} else if (step.Kind == "add") != slices.Contains(after, target) {
					step.State = domain.ItemUncertain
					step.Code = "verification_uncertain"
				} else {
					step.State = domain.ItemApplied
					step.ResultID = target
					step.AfterLabels = after
					step.Added = step.Kind == "add"
					if s.refreshLabels(ctx, plan.AccountID, row.EmailID, live.Labels) != nil {
						step.Code = "cache_refresh_failed"
					}
				}
			}
		}
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
		if plan.Steps[i].State == domain.ItemUncertain {
			// Stop dispatching after any uncertain outcome; no blind retry or dependent
			// writes. Remaining work can be reviewed in a fresh batch after reconciliation.
			for j := i + 1; j < len(plan.Steps); j++ {
				plan.Steps[j].State = domain.ItemSkipped
				plan.Steps[j].Code = "blocked_by_uncertainty"
			}
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			break
		}
	}
	return s.finish(plan)
}

// A rejected confirmation grants no Gmail write authority. Persist a valid full
// read through the existing source/chunk lifecycle so Go Back can show current
// data instead of the same obsolete selection. The original plan stays immutable.
func (s *Organization) cacheRejectedSnapshot(ctx context.Context, account domain.AccountID, row domain.OrganizationRow, live domain.Email) {
	if live.AccountID != account || live.ProviderID != row.ProviderID || live.ID != "" && live.ID != row.EmailID || live.Validate() != nil {
		return
	}
	stored, err := s.emails.Get(ctx, account, row.EmailID)
	if err != nil || stored.Email.BodyState == "complete" && live.BodyState != "complete" {
		return
	}
	live.ID = row.EmailID
	if _, err := s.emails.Upsert(ctx, live); err != nil {
		return
	}
	if s.prepareChunks != nil {
		_ = s.prepareChunks(ctx, account, live.ProviderID)
	}
}
func (s *Organization) refreshLabels(ctx context.Context, account domain.AccountID, id domain.EmailID, labels []domain.EmailLabel) error {
	stored, err := s.emails.Get(ctx, account, id)
	if err == nil {
		stored.Email.Labels = labels
		_, err = s.emails.Upsert(ctx, stored.Email)
	}
	return err
}
func (s *Organization) reconcile(ctx context.Context, plan domain.OrganizationPlan, p OrganizationProvider) (OrganizationResponse, error) {
	labels, err := listOrganizationLabels(ctx, plan.AccountID, p)
	if err != nil {
		return organizationDTO(plan), err
	}
	plan.Status = domain.ActionApplying
	plan, err = s.save(plan)
	if err != nil {
		return organizationDTO(plan), err
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.State == domain.ItemPending {
			step.State = domain.ItemSkipped
			step.Code = "recovered_pending"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		if step.State != domain.ItemInFlight && step.State != domain.ItemUncertain {
			continue
		}
		if step.State == domain.ItemInFlight {
			step.State = domain.ItemUncertain
			step.Code = "interrupted"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			step = &plan.Steps[i]
		}
		if step.Kind == "create_label" {
			if label, ok := labels[step.Name]; ok && label.Kind == domain.LabelKindUser {
				step.State = domain.ItemConflict
				step.Code = "label_exists_ownership_unknown"
			} else {
				step.State = domain.ItemFailed
				step.Code = "label_not_observed"
			}
		}
		if step.Kind != "create_label" {
			row := findOrganizationRow(plan, step.EmailID)
			live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
			if readErr != nil {
				return organizationDTO(plan), readErr
			}
			if live.ProviderID != row.ProviderID {
				return organizationDTO(plan), provider.ErrMalformed
			}
			ids, _, readErr := labelsSnapshot(plan.AccountID, live.Labels)
			if readErr != nil {
				return organizationDTO(plan), readErr
			}
			target := step.LabelID
			if target == "" {
				for _, creation := range plan.Steps {
					if creation.Kind == "create_label" && creation.Name == step.Name {
						target = creation.ResultID
					}
				}
			}
			if target == "" {
				return organizationDTO(plan), repository.ErrConflict
			}
			// An ambiguous dispatch cannot establish who added a label. Preserve it and
			// exclude it from undo, even when the desired membership is now observable.
			step.State = domain.ItemConflict
			step.Code = "membership_ownership_unknown"
			if (step.Kind == "add") != slices.Contains(ids, target) {
				step.State = domain.ItemFailed
				step.Code = "change_not_observed"
			}
			if s.refreshLabels(ctx, plan.AccountID, row.EmailID, live.Labels) != nil {
				step.Code = "cache_refresh_failed"
			}
		}
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
	}
	return s.finish(plan)
}
