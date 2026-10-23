package app

import (
	"context"
	"slices"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func TestOrganizationConfirmationConflictRefreshesReadCacheAndPreservesPlan(t *testing.T) {
	for _, change := range []string{"mail_changed", "labels_changed", "target_label_changed", "classification_changed"} {
		t.Run(change, func(t *testing.T) {
			s, f, email := organizationFixture(t)
			ctx := context.Background()
			preview := previewOrganization(t, s, email)
			original, err := s.journal.Get(ctx, "a", domain.ActionID(preview.ID))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "mail_changed":
				f.email.Subject = "Your receipt (updated synthetic source)"
			case "labels_changed":
				label := domain.EmailLabel{AccountID: "a", ProviderID: "external", Name: "External", Kind: domain.LabelKindUser}
				f.email.Labels = append(f.email.Labels, label)
				f.labels = append(f.labels, label)
			case "target_label_changed":
				f.labels = append(f.labels, domain.EmailLabel{AccountID: "a", ProviderID: "new_target", Name: preview.Rows[0].ProposedLabel, Kind: domain.LabelKindUser})
			case "classification_changed":
				categories, err := s.categories.List(ctx, "a")
				if err != nil {
					t.Fatal(err)
				}
				category := categories[0]
				category.Description += " Updated definition."
				if _, err := s.categories.Save(ctx, "a", category); err != nil {
					t.Fatal(err)
				}
			}
			result := applyOrganization(t, s, preview)
			if result.Code != "stale_preview" || result.ConflictReason != change || len(f.writes) != 0 {
				t.Fatalf("wrong conflict or unsafe dispatch: %+v", result)
			}
			saved, err := s.journal.Get(ctx, "a", domain.ActionID(preview.ID))
			if err != nil || saved.Digest != original.Digest || saved.Rows[0].InputHash != original.Rows[0].InputHash || saved.Status != domain.ActionPreview {
				t.Fatal("rejection changed approved evidence", err)
			}
			cached, err := s.emails.Get(ctx, "a", email.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "mail_changed" && cached.Email.Subject != f.email.Subject {
				t.Fatal("changed full read not reflected in cache")
			}
			if change == "labels_changed" && !slices.ContainsFunc(cached.Email.Labels, func(label domain.EmailLabel) bool { return label.ProviderID == "external" }) {
				t.Fatal("changed labels not reflected in cache")
			}
			if change != "classification_changed" {
				fresh := previewOrganization(t, s, email)
				if change == "target_label_changed" && slices.ContainsFunc(fresh.Steps, func(step OrganizationStepDTO) bool { return step.Kind == "create_label" }) {
					t.Fatal("fresh preview recreated an existing label")
				}
			}
		})
	}
}
