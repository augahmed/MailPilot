package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

// POST is dispatched exactly once. Any failure is conservatively uncertain;
// only the journal workflow may reconcile it. It never retries a write.
func (p *Provider) post(ctx context.Context, path string, payload, dst any) error {
	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return provider.ErrInvalidArgument
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return provider.ErrInvalidArgument
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return provider.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 201 {
		return provider.ErrUnavailable
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 || json.Unmarshal(data, dst) != nil {
		return provider.ErrMalformed
	}
	return nil
}
func (p *Provider) CreateLabel(ctx context.Context, name string) (domain.EmailLabel, error) {
	if (!strings.HasPrefix(name, "MailPilot/") && name != domain.PotentialPhishingLabel) || len(name) <= 10 || len(name) > 110 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return domain.EmailLabel{}, provider.ErrInvalidArgument
	}
	var l struct{ ID, Name, Type string }
	if err := p.post(ctx, "/labels", map[string]string{"name": name, "labelListVisibility": "labelShow", "messageListVisibility": "show"}, &l); err != nil {
		return domain.EmailLabel{}, err
	}
	if !validID(l.ID) || l.Name != name {
		return domain.EmailLabel{}, provider.ErrMalformed
	}
	// A successful create can omit the output-only owner type. Verify that exact
	// returned resource with a GET instead of treating a valid creation as an
	// ambiguous write. Never retry POST or infer ownership from a name lookup.
	if l.Type == "" {
		var verified struct{ ID, Name, Type string }
		if err := p.get(ctx, "/labels/"+url.PathEscape(l.ID), url.Values{}, &verified); err != nil {
			return domain.EmailLabel{}, err
		}
		if verified.ID != l.ID || verified.Name != name {
			return domain.EmailLabel{}, provider.ErrMalformed
		}
		l = verified
	}
	if l.Type != "user" {
		return domain.EmailLabel{}, provider.ErrMalformed
	}
	label := domain.EmailLabel{AccountID: p.account, ProviderID: domain.ProviderLabelID(l.ID), Name: l.Name, Kind: domain.LabelKindUser}
	p.mu.Lock()
	if p.labels != nil {
		p.labels[l.ID] = label
	}
	p.mu.Unlock()
	return label, nil
}
func (p *Provider) modify(ctx context.Context, message domain.ProviderMessageID, label domain.ProviderLabelID, add bool) error {
	if !validID(string(message)) || !validID(string(label)) {
		return provider.ErrInvalidArgument
	}
	if err := p.loadLabels(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	l, ok := p.labels[string(label)]
	p.mu.Unlock()
	if !ok || l.Kind != domain.LabelKindUser || (!strings.HasPrefix(l.Name, "MailPilot/") && l.Name != domain.PotentialPhishingLabel) {
		return provider.ErrInvalidArgument
	}
	key := "removeLabelIds"
	if add {
		key = "addLabelIds"
	}
	var result struct{ ID string }
	if err := p.post(ctx, "/messages/"+url.PathEscape(string(message))+"/modify", map[string][]string{key: {string(label)}}, &result); err != nil {
		return err
	}
	if result.ID != string(message) {
		return provider.ErrMalformed
	}
	return nil
}
func (p *Provider) ApplyLabel(ctx context.Context, m domain.ProviderMessageID, l domain.ProviderLabelID) error {
	return p.modify(ctx, m, l, true)
}
func (p *Provider) RemoveLabel(ctx context.Context, m domain.ProviderMessageID, l domain.ProviderLabelID) error {
	return p.modify(ctx, m, l, false)
}
