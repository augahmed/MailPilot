package gmail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

type TokenSource interface {
	AccessToken(context.Context, domain.AccountID) (credentials.Secret, error)
}
type Provider struct {
	account domain.AccountID
	tokens  TokenSource
	client  *http.Client
	baseURL string
	mu      sync.Mutex
	labels  map[string]domain.EmailLabel
}

var _ provider.EmailProvider = (*Provider)(nil)
var _ provider.MetadataReader = (*Provider)(nil)

func NewProvider(account domain.AccountID, tokens TokenSource) *Provider {
	return &Provider{account: account, tokens: tokens, baseURL: "https://gmail.googleapis.com/gmail/v1/users/me", client: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (*Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{CreateLabels: true, ModifyLabels: true, CreateDrafts: true}
}

// Keep credential/cancellation checks consistent across read and write paths.
// Only sanitized provider errors leave this adapter; token-source errors may
// contain credential-store or OAuth details.
func (p *Provider) accessToken(ctx context.Context) (credentials.Secret, error) {
	if err := ctx.Err(); err != nil {
		return credentials.Secret{}, err
	}
	token, err := p.tokens.AccessToken(ctx, p.account)
	if err := ctx.Err(); err != nil {
		return credentials.Secret{}, err
	}
	if err != nil || token.Empty() {
		return credentials.Secret{}, provider.ErrAuthenticationRequired
	}
	return token, nil
}
func (p *Provider) Authenticate(ctx context.Context) (provider.Account, error) {
	if _, err := p.accessToken(ctx); err != nil {
		return provider.Account{}, err
	}
	return provider.Account{ID: p.account, Provider: "gmail"}, nil
}

// GET-only transport with bounded retries and no raw errors or response logging.
func (p *Provider) get(ctx context.Context, path string, q url.Values, dst any) error {
	for attempt := 0; attempt < 3; attempt++ {
		token, err := p.accessToken(ctx)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?"+q.Encode(), nil)
		if err != nil {
			return provider.ErrInvalidArgument
		}
		request.Header.Set("Authorization", "Bearer "+token.Reveal())
		response, err := p.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return provider.ErrUnavailable
		}
		if response.StatusCode == 200 {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
			response.Body.Close()
			if readErr != nil || len(data) > 8*1024*1024 || json.Unmarshal(data, dst) != nil {
				return provider.ErrMalformed
			}
			return nil
		}
		status := response.StatusCode
		retryAfter := response.Header.Get("Retry-After")
		response.Body.Close()
		if status == 401 {
			return provider.ErrAuthenticationRequired
		}
		if status == 404 {
			return provider.ErrNotFound
		}
		if status != 429 && status < 500 {
			return provider.ErrUnavailable
		}
		if attempt == 2 {
			if status == 429 {
				return provider.ErrRateLimited
			}
			return provider.ErrUnavailable
		}
		delay := time.Duration(1<<attempt) * time.Second
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
			delay = time.Duration(min(seconds, 30)) * time.Second
		} else if deadline, err := http.ParseTime(retryAfter); err == nil {
			delay = min(max(time.Until(deadline), 0), 30*time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return provider.ErrUnavailable
}
func validID(id string) bool {
	return id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "/\\?#\r\n")
}
func (p *Provider) ListMessages(ctx context.Context, o provider.ListMessagesOptions) (provider.MessagePage, error) {
	if o.Validate() != nil || len(o.Page.PageToken) > 4096 {
		return provider.MessagePage{}, provider.ErrInvalidArgument
	}
	q := url.Values{"maxResults": {strconv.Itoa(o.Page.Limit)}, "pageToken": {o.Page.PageToken}, "labelIds": {"INBOX"}, "includeSpamTrash": {"false"}}
	for _, id := range o.LabelIDs {
		q.Add("labelIds", string(id))
	}
	if o.ReceivedAfter != nil {
		q.Set("q", "after:"+strconv.FormatInt(o.ReceivedAfter.Unix(), 10))
	}
	var response struct {
		Messages []message `json:"messages"`
		Next     string    `json:"nextPageToken"`
	}
	if err := p.get(ctx, "/messages", q, &response); err != nil {
		return provider.MessagePage{}, err
	}
	if len(response.Messages) > o.Page.Limit || len(response.Next) > 4096 {
		return provider.MessagePage{}, provider.ErrMalformed
	}
	result := provider.MessagePage{NextPageToken: response.Next}
	for _, m := range response.Messages {
		if !validID(m.ID) {
			return provider.MessagePage{}, provider.ErrMalformed
		}
		result.Messages = append(result.Messages, provider.MessageReference{AccountID: p.account, ProviderID: domain.ProviderMessageID(m.ID), ProviderThreadID: domain.ProviderThreadID(m.ThreadID)})
	}
	return result, nil
}
func (p *Provider) loadLabels(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.labels != nil {
		return nil
	}
	var response struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"labels"`
	}
	if err := p.get(ctx, "/labels", url.Values{}, &response); err != nil {
		return err
	}
	labels := map[string]domain.EmailLabel{}
	for _, l := range response.Labels {
		kind := domain.LabelKind(l.Type)
		if l.ID == "" || kind != domain.LabelKindUser && kind != domain.LabelKindSystem {
			return provider.ErrMalformed
		}
		labels[l.ID] = domain.EmailLabel{AccountID: p.account, ProviderID: domain.ProviderLabelID(l.ID), Name: l.Name, Kind: kind}
	}
	p.labels = labels
	return nil
}
func (p *Provider) FetchMessageMetadata(ctx context.Context, id domain.ProviderMessageID) (provider.MessageMetadata, error) {
	if !validID(string(id)) {
		return provider.MessageMetadata{}, provider.ErrInvalidArgument
	}
	if err := p.loadLabels(ctx); err != nil {
		return provider.MessageMetadata{}, err
	}
	var m message
	if err := p.get(ctx, "/messages/"+url.PathEscape(string(id)), url.Values{"format": {"minimal"}}, &m); err != nil {
		return provider.MessageMetadata{}, err
	}
	if m.ID != string(id) {
		return provider.MessageMetadata{}, provider.ErrMalformed
	}
	return provider.MessageMetadata{ProviderID: id, ProviderThreadID: domain.ProviderThreadID(m.ThreadID), ContentRevision: contentRevision(m), Labels: mappedLabels(p.account, m.LabelIDs, p.labelSnapshot())}, nil
}
func (p *Provider) FetchMessage(ctx context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	if !validID(string(id)) {
		return domain.Email{}, provider.ErrInvalidArgument
	}
	if err := p.loadLabels(ctx); err != nil {
		return domain.Email{}, err
	}
	var m message
	if err := p.get(ctx, "/messages/"+url.PathEscape(string(id)), url.Values{"format": {"full"}}, &m); err != nil {
		return domain.Email{}, err
	}
	if m.ID != string(id) {
		return domain.Email{}, provider.ErrMalformed
	}
	return normalize(p.account, m, p.labelSnapshot())
}
func (p *Provider) ListLabels(ctx context.Context, page provider.PageOptions) (provider.LabelPage, error) {
	if page.Validate() != nil {
		return provider.LabelPage{}, provider.ErrInvalidArgument
	}
	if page.PageToken == "" {
		p.mu.Lock()
		p.labels = nil
		p.mu.Unlock()
	}
	if err := p.loadLabels(ctx); err != nil {
		return provider.LabelPage{}, err
	}
	ids := []string{}
	snapshot := p.labelSnapshot()
	for id := range snapshot {
		ids = append(ids, id)
	}
	labels := mappedLabels(p.account, ids, snapshot)
	offset := 0
	if page.PageToken != "" {
		var err error
		offset, err = strconv.Atoi(page.PageToken)
		if err != nil || offset < 0 || offset > len(labels) {
			return provider.LabelPage{}, provider.ErrInvalidArgument
		}
	}
	end := min(offset+page.Limit, len(labels))
	result := provider.LabelPage{Labels: labels[offset:end]}
	if end < len(labels) {
		result.NextPageToken = strconv.Itoa(end)
	}
	return result, nil
}
func (*Provider) FetchThread(context.Context, domain.ProviderThreadID, provider.PageOptions) (domain.EmailThread, error) {
	return domain.EmailThread{}, provider.ErrUnsupported
}

func (p *Provider) labelSnapshot() map[string]domain.EmailLabel {
	p.mu.Lock()
	defer p.mu.Unlock()
	labels := make(map[string]domain.EmailLabel, len(p.labels))
	for id, l := range p.labels {
		labels[id] = l
	}
	return labels
}
