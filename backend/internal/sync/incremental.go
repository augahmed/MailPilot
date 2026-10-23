package sync

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

func (s *Service) syncChanges(ctx context.Context, a domain.AccountID, g uint64, reader Reader, changes provider.ChangeReader, recovering bool) error {
	state, err := s.checkpoints.Load(ctx, a)
	if err != nil {
		return err
	}
	commit := func(next repository.SyncCheckpoint, email *domain.Email, deleted domain.ProviderMessageID) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.checkpoints.Commit(ctx, a, state, next, email, deleted); err != nil {
			return err
		}
		state = next
		return nil
	}
	mode := func(value string) {
		s.update(g, func(p *Progress) { p.Mode = value; p.SnapshotLimited = state.Limited })
	}
	more := func() { s.update(g, func(p *Progress) { p.More = true }) }
	if state.Phase == "history" {
		mode("incremental")
	} else {
		mode("recovery")
	}
	budget := s.limit
	observed := map[domain.ProviderMessageID]bool{}
	observe := func(id domain.ProviderMessageID, deleted bool, next repository.SyncCheckpoint) error {
		if err := s.observe(ctx, a, g, reader, changes, &state, next, id, deleted, false); err != nil {
			return err
		}
		observed[id] = true
		budget--
		return nil
	}
	// Expiration recovery reconciles every cached identity with authoritative GETs.
	// Keyset pagination is stable when earlier rows are tombstoned during the scan.
	recoverCache := func() error {
		for budget > 0 {
			ids, err := s.checkpoints.CachedIDs(ctx, a, state.After, min(budget, 100))
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				next := state
				next.Phase = "history"
				next.After = ""
				return commit(next, nil, "")
			}
			for _, id := range ids {
				next := state
				next.After = string(id)
				if err := observe(id, false, next); err != nil {
					return err
				}
			}
		}
		more()
		return nil
	}
	if state.Phase == "reconcile" {
		if err := recoverCache(); err != nil {
			return err
		}
		if state.Phase == "reconcile" {
			return nil
		}
	}
	if state.Phase != "history" {
		return repository.ErrStorage
	}
	// A page token is only used with the original start cursor. Commit record
	// cursors as they finish; on interruption resume from the last durable record.
	start := state.Cursor
	token := ""
	pages := map[string]bool{}
	for pageCount := 0; pageCount < 1000; pageCount++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if pages[token] {
			return provider.ErrMalformed
		}
		pages[token] = true
		page, err := changes.ListChanges(ctx, start, provider.PageOptions{Limit: 100, PageToken: token})
		if errors.Is(err, provider.ErrCheckpointExpired) {
			if recovering {
				return err
			}
			cursor, err := changes.CurrentCheckpoint(ctx)
			if err != nil {
				return err
			}
			next := repository.SyncCheckpoint{Cursor: cursor, Phase: "reconcile", Limited: true, InboxVersion: 1, RetryIDs: state.RetryIDs}
			if err := commit(next, nil, ""); err != nil {
				return err
			}
			mode("recovery")
			more()
			// Begin recovery immediately when this job has not spent its change
			// budget. Otherwise the next manual run resumes the durable phase.
			if budget == s.limit {
				return s.syncChanges(ctx, a, g, reader, changes, true)
			}
			return nil
		}
		if err != nil {
			return err
		}
		for _, record := range page.Records {
			offset := 0
			if state.Record != "" {
				if state.Record != record.Cursor || state.Offset > len(record.Changes) {
					return provider.ErrMalformed
				}
				offset = state.Offset
			}
			for i := offset; i < len(record.Changes); i++ {
				if budget <= 0 {
					more()
					return nil
				}
				change := record.Changes[i]
				if change.ProviderID == "" {
					return provider.ErrMalformed
				}
				next := state
				next.Record = record.Cursor
				next.Offset = i + 1
				if i+1 == len(record.Changes) {
					next.Cursor = record.Cursor
					next.Record = ""
					next.Offset = 0
				}
				if err := observe(change.ProviderID, change.Deleted, next); err != nil {
					return err
				}
			}
			if len(record.Changes) == 0 {
				next := state
				next.Cursor = record.Cursor
				next.Record = ""
				next.Offset = 0
				if err := commit(next, nil, ""); err != nil {
					return err
				}
			}
		}
		if page.NextPageToken == "" {
			if state.Record != "" || page.Cursor == "" {
				return provider.ErrMalformed
			}
			next := state
			next.Cursor = page.Cursor
			if err := commit(next, nil, ""); err != nil {
				return err
			}
			// Retry a bounded rotation of failed reads without blocking later mail.
			queued := append([]domain.ProviderMessageID(nil), state.RetryIDs...)
			for _, id := range queued[:min(len(queued), 50)] {
				if budget <= 0 {
					break
				}
				if observed[id] {
					continue
				}
				next := withoutRetry(state, id)
				if err := observe(id, false, next); err != nil {
					return err
				}
			}
			if len(state.RetryIDs) > 0 {
				more()
			}
			// History has priority. Use only the remaining observation budget to
			// retry cached incomplete bodies, which may recover without a new Gmail
			// history event. Persist the rotation with each source observation so
			// interruption cannot skip a failed read or starve later identities.
			for budget > 0 {
				ids, err := s.checkpoints.IncompleteIDs(ctx, a, state.RetryAfter, min(budget, 100))
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					next := state
					next.RetryAfter = ""
					if err := commit(next, nil, ""); err != nil {
						return err
					}
					break
				}
				for _, id := range ids {
					next := state
					next.RetryAfter = string(id)
					if observed[id] {
						if err := commit(next, nil, ""); err != nil {
							return err
						}
						continue
					}
					if err := observe(id, false, next); err != nil {
						return err
					}
				}
			}
			remaining, err := s.checkpoints.IncompleteIDs(ctx, a, "", 1)
			if err != nil {
				return err
			}
			if len(remaining) > 0 {
				more()
			}
			return nil
		}
		if budget <= 0 {
			more()
			return nil
		}
		token = page.NextPageToken
	}
	more()
	return nil
}
