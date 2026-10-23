package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"reflect"
)

type syncRepository struct{ store *Store }

func (s *Store) SyncCheckpoints() repository.SyncRepository { return &syncRepository{s} }
func (r *syncRepository) Load(ctx context.Context, a domain.AccountID) (repository.SyncCheckpoint, error) {
	var state repository.SyncCheckpoint
	var raw string
	err := r.store.db.QueryRowContext(ctx, "SELECT state_json FROM sync_checkpoints WHERE account_id=?", a).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, storageError(err)
	}
	if json.Unmarshal([]byte(raw), &state) != nil {
		return state, repository.ErrStorage
	}
	return state, nil
}
func (r *syncRepository) Commit(ctx context.Context, a domain.AccountID, expected, next repository.SyncCheckpoint, email *domain.Email, deleted domain.ProviderMessageID) error {
	if next.InboxVersion < 0 || next.InboxVersion > 1 || len(next.InboxPageToken) > 4096 || len(next.InboxPending) > 100 || len(next.RetryIDs) > 500 {
		return repository.ErrInvalid
	}
	for _, ids := range [][]domain.ProviderMessageID{next.InboxPending, next.RetryIDs} {
		seen := map[domain.ProviderMessageID]bool{}
		for _, id := range ids {
			if id == "" || len(id) > 1024 || seen[id] {
				return repository.ErrInvalid
			}
			seen[id] = true
		}
	}
	if a == "" || len(next.Cursor) > 4096 || len(next.After) > 1024 || len(next.RetryAfter) > 1024 || len(next.Record) > 4096 || next.Offset < 0 || next.Offset > 10000 || (next.Phase != "snapshot" && next.Phase != "reconcile" && next.Phase != "history") || (email != nil && (email.AccountID != a || email.Validate() != nil || deleted != "")) {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		var raw string
		var current repository.SyncCheckpoint
		err := tx.QueryRowContext(ctx, "SELECT state_json FROM sync_checkpoints WHERE account_id=?", a).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && json.Unmarshal([]byte(raw), &current) != nil {
			return repository.ErrStorage
		}
		if !reflect.DeepEqual(current, expected) {
			return repository.ErrConflict
		}
		if email != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE emails SET remote_deleted=0 WHERE account_id=? AND provider_message_id=? AND remote_deleted<>0", a, email.ProviderID); err != nil {
				return err
			}
			if _, err := upsertEmail(ctx, tx, *email); err != nil {
				return err
			}
		}
		if deleted != "" {
			var id string
			err := tx.QueryRowContext(ctx, "SELECT id FROM emails WHERE account_id=? AND provider_message_id=?", a, deleted).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				for _, table := range []string{"email_passage_vectors", "email_retrieval_indexes", "email_labels", "email_categories", "email_embeddings", "classification_results", "classification_runs", "phishing_results"} {
					if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE account_id=? AND email_id=?", a, id); err != nil {
						return err
					}
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM email_addresses WHERE email_id=?", id); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE emails SET remote_deleted=1,subject='',body_text='',body_html=NULL,headers_json='[]',attachments_json='[]',content_hash='',content_revision='',body_state='unavailable',updated_at=? WHERE account_id=? AND id=?`, nowMillis(), a, id); err != nil {
					return err
				}
			}
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sync_checkpoints(account_id,state_json,updated_at) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET state_json=excluded.state_json,updated_at=excluded.updated_at`, a, string(encoded), nowMillis())
		return err
	})
}
func (r *syncRepository) CachedIDs(ctx context.Context, a domain.AccountID, after string, limit int) ([]domain.ProviderMessageID, error) {
	return r.cachedIDs(ctx, a, after, limit, false)
}
func (r *syncRepository) IncompleteIDs(ctx context.Context, a domain.AccountID, after string, limit int) ([]domain.ProviderMessageID, error) {
	return r.cachedIDs(ctx, a, after, limit, true)
}
func (r *syncRepository) cachedIDs(ctx context.Context, a domain.AccountID, after string, limit int, incomplete bool) ([]domain.ProviderMessageID, error) {
	if a == "" || limit < 1 || limit > 100 {
		return nil, repository.ErrInvalid
	}
	query := "SELECT provider_message_id FROM emails WHERE account_id=? AND remote_deleted=0 AND provider_message_id>?"
	if incomplete {
		query += " AND body_state<>'complete'"
	}
	rows, err := r.store.db.QueryContext(ctx, query+" ORDER BY provider_message_id LIMIT ?", a, after, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	ids := []domain.ProviderMessageID{}
	for rows.Next() {
		var id domain.ProviderMessageID
		if err := rows.Scan(&id); err != nil {
			return nil, storageError(err)
		}
		ids = append(ids, id)
	}
	return ids, storageError(rows.Err())
}
