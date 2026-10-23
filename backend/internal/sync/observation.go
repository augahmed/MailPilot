package sync

import (
	"context"
	"errors"
	"slices"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

func (s *Service) commitCheckpoint(ctx context.Context, a domain.AccountID, state *repository.SyncCheckpoint, next repository.SyncCheckpoint, email *domain.Email, deleted domain.ProviderMessageID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkpoints.Commit(ctx, a, *state, next, email, deleted); err != nil {
		return err
	}
	*state = next
	return nil
}

func withoutRetry(state repository.SyncCheckpoint, id domain.ProviderMessageID) repository.SyncCheckpoint {
	state.RetryIDs = slices.DeleteFunc(slices.Clone(state.RetryIDs), func(value domain.ProviderMessageID) bool { return value == id })
	if len(state.RetryIDs) == 0 {
		state.RetryIDs = nil
	}
	return state
}

// Observe one authoritative read. A per-message read failure is journaled with
// its paging position; account, quota, cancellation and storage failures stop.
func (s *Service) observe(ctx context.Context, a domain.AccountID, g uint64, reader Reader, changes provider.ChangeReader, state *repository.SyncCheckpoint, next repository.SyncCheckpoint, id domain.ProviderMessageID, deleted, inbox bool) error {
	s.update(g, func(p *Progress) { p.Discovered++ })
	old, err := s.emails.GetByProviderID(ctx, a, id)
	exists := err == nil
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	var email *domain.Email
	if !deleted {
		var value domain.Email
		// A listed, uncached Inbox message needs one full GET, not a minimal GET
		// followed by the same message's full GET. The page itself supplies the bounded Inbox scope.
		if inbox && !exists {
			value, err = reader.FetchMessage(ctx, id)
			if err == nil {
				if value.AccountID != a || value.ProviderID != id || value.Validate() != nil {
					err = provider.ErrMalformed
				} else {
					email = &value
				}
			}
		} else {
			var metadata provider.MessageMetadata
			metadata, err = changes.FetchMessageMetadata(ctx, id)
			if err == nil {
				if metadata.ProviderID != id {
					err = provider.ErrMalformed
				} else if exists || metadata.InSyncScope {
					if exists && old.Email.BodyState == "complete" && metadata.ContentRevision != "" && metadata.ContentRevision == old.Email.ContentRevision {
						value = old.Email
						value.Labels = metadata.Labels
						value.ProviderThreadID = metadata.ProviderThreadID
					} else {
						value, err = reader.FetchMessage(ctx, id)
					}
					if err == nil {
						if value.AccountID != a || value.ProviderID != id || value.Validate() != nil {
							err = provider.ErrMalformed
						} else {
							email = &value
						}
					}
				}
			}
		}
		if errors.Is(err, provider.ErrNotFound) {
			deleted = true
			err = nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, provider.ErrAuthenticationRequired) || errors.Is(err, provider.ErrRateLimited) || errors.Is(err, repository.ErrStorage) || errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrInvalid) {
				return err
			}
			next = withoutRetry(next, id)
			if len(next.RetryIDs) >= 500 {
				return err
			}
			next.RetryIDs = append(next.RetryIDs, id)
			if commitErr := s.commitCheckpoint(ctx, a, state, next, nil, ""); commitErr != nil {
				return commitErr
			}
			s.update(g, func(p *Progress) { p.Errors++; p.Code = errorCode(err); p.More = true })
			return nil
		}
	}
	next = withoutRetry(next, id)
	var removed domain.ProviderMessageID
	if deleted {
		removed = id
	}
	if err := s.commitCheckpoint(ctx, a, state, next, email, removed); err != nil {
		return err
	}
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
			p.More = true
		}
	})
	// Count committed mail even when cancellation interrupts local preparation.
	if email != nil && s.prepareChunks != nil {
		if err := s.prepareChunks(ctx, a, id); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.update(g, func(p *Progress) { p.Errors++; p.Code = "chunking_failed" })
		}
	}
	return nil
}
