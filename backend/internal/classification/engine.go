// Package classification predicts local categories without changing mailbox membership.
package classification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"mailpilot.local/core/internal/domain"
)

// ClassificationEngine is the boundary between review use cases and a classifier.
// An empty Results slice is a valid assessment: the email remains unclassified.
type ClassificationEngine interface {
	Name() string
	Version() string
	Classify(context.Context, domain.Email, []domain.EmailCategory) (domain.ClassificationRun, error)
}

// InputHash covers exactly the locally available inputs, including category
// definitions and label names. It deliberately excludes provider-specific IDs,
// attachments, local assignments, and recipients. HTML is included for embedding fallback.
func InputHash(email domain.Email, categories []domain.EmailCategory) string {
	type label struct {
		Name string
		Kind domain.LabelKind
	}
	type category struct {
		ID                domain.CategoryID
		Name, Description string
		Enabled           bool
		UpdatedAt         int64
	}
	labels := make([]label, 0, len(email.Labels))
	for _, l := range email.Labels {
		labels = append(labels, label{l.Name, l.Kind})
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Name != labels[j].Name {
			return labels[i].Name < labels[j].Name
		}
		return labels[i].Kind < labels[j].Kind
	})
	definitions := make([]category, 0, len(categories))
	for _, c := range categories {
		definitions = append(definitions, category{c.ID, c.Name, c.Description, c.Enabled, c.UpdatedAt.UnixMilli()})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	data, _ := json.Marshal(struct {
		Version                  int
		HTML                     *string
		Sender                   domain.EmailAddress
		Subject, Body, BodyState string
		Labels                   []label
		Categories               []category
	}{2, email.BodyHTML, email.Sender, email.Subject, email.BodyText, email.BodyState, labels, definitions})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
