package gmail

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"net/url"
	"strconv"
)

func historyID(s string) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatUint(n, 10) == s
}
func (p *Provider) CurrentCheckpoint(ctx context.Context) (string, error) {
	var response struct {
		HistoryID string `json:"historyId"`
	}
	if err := p.get(ctx, "/profile", url.Values{}, &response); err != nil {
		return "", err
	}
	if _, ok := historyID(response.HistoryID); !ok {
		return "", provider.ErrMalformed
	}
	return response.HistoryID, nil
}
func (p *Provider) ListChanges(ctx context.Context, cursor string, page provider.PageOptions) (provider.ChangePage, error) {
	start, ok := historyID(cursor)
	if !ok {
		return provider.ChangePage{}, provider.ErrCheckpointExpired
	}
	if page.Validate() != nil || len(page.PageToken) > 4096 {
		return provider.ChangePage{}, provider.ErrInvalidArgument
	}
	type event struct {
		Message message `json:"message"`
	}
	var response struct {
		History []struct {
			ID            string    `json:"id"`
			Messages      []message `json:"messages"`
			Added         []event   `json:"messagesAdded"`
			Deleted       []event   `json:"messagesDeleted"`
			LabelsAdded   []event   `json:"labelsAdded"`
			LabelsRemoved []event   `json:"labelsRemoved"`
		} `json:"history"`
		Next      string `json:"nextPageToken"`
		HistoryID string `json:"historyId"`
	}
	// No label filter: filtering INBOX can hide its removal and permanent deletions.
	err := p.get(ctx, "/history", url.Values{"startHistoryId": {cursor}, "pageToken": {page.PageToken}, "maxResults": {strconv.Itoa(page.Limit)}}, &response)
	if errors.Is(err, provider.ErrNotFound) || errors.Is(err, provider.ErrInvalidArgument) {
		return provider.ChangePage{}, provider.ErrCheckpointExpired
	}
	if err != nil {
		return provider.ChangePage{}, err
	}
	final, ok := historyID(response.HistoryID)
	if !ok || final < start || len(response.History) > page.Limit || len(response.Next) > 4096 {
		return provider.ChangePage{}, provider.ErrMalformed
	}
	result := provider.ChangePage{Cursor: response.HistoryID, NextPageToken: response.Next}
	for _, record := range response.History {
		current, ok := historyID(record.ID)
		if !ok || current <= start || current > final {
			return provider.ChangePage{}, provider.ErrMalformed
		}
		start = current
		changes := []provider.MessageChange{}
		seen := map[string]int{}
		add := func(id string, deleted bool) bool {
			if !validID(id) {
				return false
			}
			if index, ok := seen[id]; ok {
				changes[index].Deleted = changes[index].Deleted || deleted
				return true
			}
			seen[id] = len(changes)
			changes = append(changes, provider.MessageChange{ProviderID: domain.ProviderMessageID(id), Deleted: deleted})
			return len(changes) <= 10000
		}
		for _, group := range [][]event{record.Added, record.LabelsAdded, record.LabelsRemoved} {
			for _, e := range group {
				if !add(e.Message.ID, false) {
					return provider.ChangePage{}, provider.ErrMalformed
				}
			}
		}
		for _, e := range record.Deleted {
			if !add(e.Message.ID, true) {
				return provider.ChangePage{}, provider.ErrMalformed
			}
		}
		// Generic messages can include changes without a typed entry. Dedupe rather
		// than ignoring them, so future/unknown content changes are refreshed safely.
		for _, m := range record.Messages {
			if !add(m.ID, false) {
				return provider.ChangePage{}, provider.ErrMalformed
			}
		}
		result.Records = append(result.Records, provider.ChangeRecord{Cursor: record.ID, Changes: changes})
	}
	return result, nil
}
