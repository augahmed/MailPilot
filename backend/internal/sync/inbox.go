package sync

import (
	"context"
	"errors"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

func (s *Service) incremental(ctx context.Context, a domain.AccountID, g uint64, reader Reader, changes provider.ChangeReader) error {
	state, err := s.checkpoints.Load(ctx, a)
	if err != nil {
		return err
	}
	initial := state.Phase == ""
	if initial {
		cursor, err := changes.CurrentCheckpoint(ctx)
		if err != nil {
			return err
		}
		cached, err := s.checkpoints.CachedIDs(ctx, a, "", 1)
		if err != nil {
			return err
		}
		next := repository.SyncCheckpoint{Cursor: cursor, Phase: "snapshot", InboxVersion: 1, ReconcileAfterSnapshot: len(cached) > 0}
		if err := s.commitCheckpoint(ctx, a, &state, next, nil, ""); err != nil {
			return err
		}
	} else if state.InboxVersion == 0 {
		next := state
		next.InboxVersion = 1
		// A legacy completed, unlimited scan already reached the end of Inbox.
		next.InboxComplete = state.Phase == "history" && !state.Limited
		next.InboxSkipCached = state.Phase == "history" && state.Limited
		if err := s.commitCheckpoint(ctx, a, &state, next, nil, ""); err != nil {
			return err
		}
	}
	if state.Phase == "snapshot" {
		mode := "recovery"
		if initial {
			mode = "initial"
		}
		s.update(g, func(p *Progress) { p.Mode = mode })
		if err := s.scanInbox(ctx, a, g, reader, changes, &state); err != nil {
			return err
		}
		next := state
		next.Phase = "history"
		if next.ReconcileAfterSnapshot {
			next.Phase = "reconcile"
			next.ReconcileAfterSnapshot = false
		}
		if err := s.commitCheckpoint(ctx, a, &state, next, nil, ""); err != nil {
			return err
		}
		s.update(g, func(p *Progress) { p.More = true }) // Replay changes since the pre-scan anchor on the next manual sync.
		return nil
	}
	if err := s.syncChanges(ctx, a, g, reader, changes, false); err != nil {
		return err
	}
	state, err = s.checkpoints.Load(ctx, a)
	if err != nil {
		return err
	}
	if err := s.scanInbox(ctx, a, g, reader, changes, &state); err != nil {
		return err
	}
	return nil
}

// Each manual run has a separate bounded Inbox scan. History maintenance does
// not consume this allowance, so updates cannot starve older Inbox imports.
func (s *Service) scanInbox(ctx context.Context, a domain.AccountID, g uint64, reader Reader, changes provider.ChangeReader, state *repository.SyncCheckpoint) error {
	pages := map[string]bool{}
	seen := map[domain.ProviderMessageID]bool{}
	restarted := false
	for len(seen) < s.limit && !state.InboxComplete {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(state.InboxPending) == 0 {
			token := state.InboxPageToken
			if pages[token] {
				return provider.ErrMalformed
			}
			pages[token] = true
			if len(pages) > 1000 {
				return provider.ErrMalformed
			}
			page, err := reader.ListMessages(ctx, provider.ListMessagesOptions{Page: provider.PageOptions{Limit: 100, PageToken: token}})
			if errors.Is(err, provider.ErrInvalidArgument) && token != "" {
				// Provider tokens can expire. Restart safely without interpreting absence
				// from the listing as deletion; already cached sources remain idempotent.
				if restarted {
					return err
				}
				restarted = true
				pages = map[string]bool{}
				next := *state
				next.InboxPageToken = ""
				next.InboxSkipCached = true
				if err := s.commitCheckpoint(ctx, a, state, next, nil, ""); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if len(page.Messages) > 100 || len(page.Messages) == 0 && page.NextPageToken != "" || page.NextPageToken != "" && pages[page.NextPageToken] {
				return provider.ErrMalformed
			}
			next := *state
			next.InboxPageToken = page.NextPageToken
			next.InboxPending = nil
			ids := map[domain.ProviderMessageID]bool{}
			for _, ref := range page.Messages {
				if ref.AccountID != a || ref.ProviderID == "" {
					return provider.ErrMalformed
				}
				if !ids[ref.ProviderID] {
					next.InboxPending = append(next.InboxPending, ref.ProviderID)
					ids[ref.ProviderID] = true
				}
			}
			next.InboxComplete = len(next.InboxPending) == 0 && next.InboxPageToken == ""
			next.Limited = !next.InboxComplete
			if err := s.commitCheckpoint(ctx, a, state, next, nil, ""); err != nil {
				return err
			}
			if next.InboxComplete {
				break
			}
		}
		id := state.InboxPending[0]
		next := *state
		next.InboxPending = append([]domain.ProviderMessageID(nil), next.InboxPending[1:]...)
		next.InboxComplete = len(next.InboxPending) == 0 && next.InboxPageToken == ""
		next.Limited = !next.InboxComplete
		if state.InboxSkipCached {
			// Walk past cached messages after a token reset so repeated manual
			// syncs do not get stuck refreshing the same newest 500 messages.
			_, err := s.emails.GetByProviderID(ctx, a, id)
			if err == nil {
				if err := s.commitCheckpoint(ctx, a, state, next, nil, ""); err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, repository.ErrNotFound) {
				return err
			}
		}
		if seen[id] {
			if err := s.commitCheckpoint(ctx, a, state, next, nil, ""); err != nil {
				return err
			}
		} else {
			seen[id] = true
			if err := s.observe(ctx, a, g, reader, changes, state, next, id, false, true); err != nil {
				return err
			}
		}
	}
	s.update(g, func(p *Progress) {
		p.SnapshotLimited = !state.InboxComplete
		p.Capped = !state.InboxComplete
		p.More = p.More || !state.InboxComplete || len(state.RetryIDs) > 0
	})
	return nil
}
