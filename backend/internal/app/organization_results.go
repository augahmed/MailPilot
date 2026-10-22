package app

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

// The immutable plan keeps its before-label snapshot. Show separately the cached
// labels refreshed from Gmail after a verified write, including on status reload.
func (s *Organization) withCachedLabels(ctx context.Context, result OrganizationResponse) OrganizationResponse {
	if result.State == "preview" || result.State == "dry_run" || result.State == "idle" {
		return result
	}
	verified := map[string]bool{}
	for _, step := range result.Steps {
		if (step.Kind == "add" || step.Kind == "remove") && step.State == "applied" && step.Code == "" {
			verified[step.EmailID] = true
		}
	}
	for i := range result.Rows {
		row := &result.Rows[i]
		if !verified[row.EmailID] {
			continue
		}
		stored, err := s.emails.Get(ctx, domain.AccountID(result.AccountID), domain.EmailID(row.EmailID))
		if err != nil {
			continue
		}
		labels := []string{}
		for _, label := range stored.Email.Labels {
			labels = append(labels, shortText(label.Name, 100))
		}
		row.UpdatedLabels = &labels
	}
	bounded := boundedOrganization(result)
	if bounded.Code == "validation" {
		// Optional display enrichment must not hide a valid saved action or its
		// recovery controls when the original preview was close to the frame cap.
		for i := range result.Rows {
			result.Rows[i].UpdatedLabels = nil
		}
		return boundedOrganization(result)
	}
	return bounded
}
