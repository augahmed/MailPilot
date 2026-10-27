package demo

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

func (p *Mailbox) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (p *Mailbox) ListLabels(ctx context.Context, page provider.PageOptions) (provider.LabelPage, error) {
	if p.Account != AccountID || page.Validate() != nil || page.PageToken != "" {
		return provider.LabelPage{}, provider.ErrInvalidArgument
	}
	return provider.LabelPage{Labels: []domain.EmailLabel{Inbox()}}, ctx.Err()
}
func (p *Mailbox) FetchMessageMetadata(ctx context.Context, id domain.ProviderMessageID) (provider.MessageMetadata, error) {
	e, err := p.FetchMessage(ctx, id)
	return provider.MessageMetadata{ProviderID: e.ProviderID, ProviderThreadID: e.ProviderThreadID, ContentRevision: e.ContentRevision, Labels: e.Labels}, err
}
func (*Mailbox) CreateLabel(context.Context, string) (domain.EmailLabel, error) {
	return domain.EmailLabel{}, provider.ErrUnsupported
}
func (*Mailbox) ApplyLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error {
	return provider.ErrUnsupported
}
func (*Mailbox) RemoveLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error {
	return provider.ErrUnsupported
}
