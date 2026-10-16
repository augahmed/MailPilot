package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type aiSettingsRepository struct{ store *Store }

func (s *Store) AISettings() ai.SettingsStore { return &aiSettingsRepository{store: s} }

// Keep the existing table and migration checksums. Its historical name is not
// part of the shared AI settings contract.
func (r *aiSettingsRepository) Settings(ctx context.Context, a domain.AccountID) (ai.Settings, error) {
	var s ai.Settings
	e := r.store.db.QueryRowContext(ctx, `SELECT model,enabled FROM reply_ai_settings WHERE account_id=?`, a).Scan(&s.Model, &s.Enabled)
	if errors.Is(e, sql.ErrNoRows) {
		return s, nil
	}
	if e != nil {
		return s, storageError(e)
	}
	return s, nil
}
func (r *aiSettingsRepository) SaveSettings(ctx context.Context, a domain.AccountID, s ai.Settings) error {
	if a == "" || len(s.Model) > 128 || s.Enabled && s.Model == "" {
		return repository.ErrInvalid
	}
	_, e := r.store.db.ExecContext(ctx, `INSERT INTO reply_ai_settings VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET model=excluded.model,enabled=excluded.enabled`, a, s.Model, s.Enabled)
	if e != nil {
		return storageError(e)
	}
	return nil
}
