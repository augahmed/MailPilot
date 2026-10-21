package app

import (
	"context"
	"errors"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

// CachedChunker projects committed cache snapshots. It owns no goroutine,
// inference, network access or alternative chunk store.
type CachedChunker struct {
	emails  repository.EmailRepository
	chunks  repository.ChunkRepository
	chunker *mailtext.Chunker
}

func NewCachedChunker(emails repository.EmailRepository, chunks repository.ChunkRepository) (*CachedChunker, error) {
	chunker, err := mailtext.NewChunker()
	if err != nil {
		return nil, err
	}
	return &CachedChunker{emails, chunks, chunker}, nil
}

// Prepare is called after a manual sync commits cached mail/checkpoints. A
// failure cannot lose cached mail or roll back Gmail sync. Replace rechecks the
// source revision atomically and creates only pending embedding rows, no vectors.
func (c *CachedChunker) Prepare(ctx context.Context, a domain.AccountID, id domain.ProviderMessageID) error {
	stored, err := c.emails.GetByProviderID(ctx, a, id)
	if err != nil {
		return err
	}
	state, err := c.chunks.State(ctx, a, stored.Email.ID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if err == nil && state.Status != repository.IndexStale && state.SourceRevision == stored.ContentHash && state.PreprocessingVersion == mailtext.PreprocessingVersion && state.IndexVersion == mailtext.ChunkIndexVersion && state.Model == mailtext.ChunkModel() {
		return nil
	}
	snapshot, err := c.chunker.Build(ctx, stored)
	if err != nil {
		return err
	}
	_, err = c.chunks.Replace(ctx, snapshot)
	return err
}

// PrepareCached backfills a bounded number of old/stale cached messages during a
// manual sync, including after an interrupted post-commit projection. It scans at
// most 10,000 cached summaries and attempts at most limit messages. Current recipes
// need no body read or tokenization. It does not widen Gmail sync scope.
func (c *CachedChunker) PrepareCached(ctx context.Context, a domain.AccountID, limit int) (int, error) {
	if a == "" || limit < 1 || limit > 10000 {
		return 0, repository.ErrInvalid
	}
	attempted, failed := 0, 0
	for offset := 0; offset < 10000 && attempted < limit; offset += 100 {
		rows, err := c.emails.ListSummaries(ctx, a, repository.Page{Limit: 100, Offset: offset})
		if err != nil {
			return failed, err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return failed, err
			}
			state, err := c.chunks.State(ctx, a, row.ID)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return failed, err
			}
			if err == nil && state.Status != repository.IndexStale && state.PreprocessingVersion == mailtext.PreprocessingVersion && state.IndexVersion == mailtext.ChunkIndexVersion && state.Model == mailtext.ChunkModel() {
				continue
			}
			attempted++
			stored, err := c.emails.Get(ctx, a, row.ID)
			if err == nil {
				var snapshot repository.ChunkSnapshot
				snapshot, err = c.chunker.Build(ctx, stored)
				if err == nil {
					_, err = c.chunks.Replace(ctx, snapshot)
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return failed, ctx.Err()
				}
				failed++
			}
			if attempted == limit {
				break
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return failed, nil
}
