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
	if state.Phase == "" {
		cursor, err := changes.CurrentCheckpoint(ctx)
		if err != nil {
			return err
		}
		// Also reconcile existing caches created before checkpoints were implemented.
		if err := commit(repository.SyncCheckpoint{Phase: "reconcile", Cursor: cursor}, nil, ""); err != nil {
			return err
		}
		mode("initial")
	} else if state.Phase == "history" {
		mode("incremental")
	} else {
		mode("recovery")
	}
	budget := s.limit
	observe := func(id domain.ProviderMessageID, deleted bool, next repository.SyncCheckpoint) error {
		s.update(g, func(p *Progress) { p.Discovered++ })
		old, err := s.emails.GetByProviderID(ctx, a, id)
		exists := err == nil
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		var email *domain.Email
		if !deleted {
			metadata, err := changes.FetchMessageMetadata(ctx, id)
			if errors.Is(err, provider.ErrNotFound) {
				deleted = true
			} else if err != nil {
				return err
			} else {
				if metadata.ProviderID != id {
					return provider.ErrMalformed
				}
				if exists || metadata.InSyncScope {
					var value domain.Email
					if exists && old.Email.BodyState == "complete" && metadata.ContentRevision != "" && metadata.ContentRevision == old.Email.ContentRevision {
						value = old.Email
						value.Labels = metadata.Labels
						value.ProviderThreadID = metadata.ProviderThreadID
					} else {
						value, err = reader.FetchMessage(ctx, id)
						if errors.Is(err, provider.ErrNotFound) {
							deleted = true
						} else if err != nil {
							return err
						}
					}
					if !deleted {
						if value.AccountID != a || value.ProviderID != id || value.Validate() != nil {
							return provider.ErrMalformed
						}
						email = &value
					}
				}
			}
		}
		var removed domain.ProviderMessageID
		if deleted {
			removed = id
		}
		if err := commit(next, email, removed); err != nil {
			return err
		}
		budget--
		s.update(g, func(p *Progress) {
			if deleted && exists {
				p.Removed++
			} else if email != nil {
				if exists {
					p.Updated++
				} else {
					p.Imported++
				}
			}
			if email != nil && email.BodyState != "complete" {
				p.Errors++
				p.Code = "body_unavailable"
			}
		})
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
				next.Phase = "snapshot"
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
	if state.Phase == "snapshot" && budget != s.limit {
		more()
		return nil
	}
	if state.Phase == "snapshot" {
		// Preserve the bounded initial recent-Inbox import. On partial failure retry
		// from its beginning with the same pre-scan anchor; never infer deletions.
		modeValue := s.Status().Mode
		if modeValue != "initial" {
			mode("recovery")
		}
		seen := map[domain.ProviderMessageID]bool{}
		pages := map[string]bool{}
		token := ""
		limited := false
		for len(seen) < s.limit {
			if err := ctx.Err(); err != nil {
				return err
			}
			if pages[token] {
				return provider.ErrMalformed
			}
			pages[token] = true
			size := min(100, s.limit-len(seen))
			page, err := reader.ListMessages(ctx, provider.ListMessagesOptions{Page: provider.PageOptions{Limit: size, PageToken: token}})
			if err != nil {
				return err
			}
			if len(page.Messages) > size || len(page.Messages) == 0 && page.NextPageToken != "" || len(pages) > s.limit+1 {
				return provider.ErrMalformed
			}
			for _, ref := range page.Messages {
				if ref.AccountID != a || ref.ProviderID == "" {
					return provider.ErrMalformed
				}
				if seen[ref.ProviderID] {
					continue
				}
				seen[ref.ProviderID] = true
				if err := observe(ref.ProviderID, false, state); err != nil {
					return err
				}
			}
			limited = page.NextPageToken != ""
			if !limited || len(seen) >= s.limit {
				break
			}
			token = page.NextPageToken
		}
		next := state
		next.Phase = "history"
		next.Limited = limited
		if err := commit(next, nil, ""); err != nil {
			return err
		}
		s.update(g, func(p *Progress) { p.Capped = limited; p.SnapshotLimited = limited; p.More = true })
		return nil // Next manual run replays all changes since the pre-scan anchor.
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
			cursor, err := changes.CurrentCheckpoint(ctx)
			if err != nil {
				return err
			}
			next := repository.SyncCheckpoint{Cursor: cursor, Phase: "reconcile", Limited: state.Limited}
			if err := commit(next, nil, ""); err != nil {
				return err
			}
			mode("recovery")
			more()
			// Begin recovery immediately when this job has not spent its change
			// budget. Otherwise the next manual run resumes the durable phase.
			if budget == s.limit {
				return s.incremental(ctx, a, g, reader, changes)
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
			return commit(next, nil, "")
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
