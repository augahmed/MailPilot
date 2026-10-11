package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func (r *accountRepository) Upsert(ctx context.Context, a domain.Account) (domain.AccountID, error) {
	if strings.TrimSpace(a.Provider) == "" || a.ProviderAccountID == "" {
		return "", repository.ErrInvalid
	}
	var id domain.AccountID
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE provider=? AND provider_account_id=?", a.Provider, a.ProviderAccountID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = a.ID
			if id == "" {
				id = domain.AccountID(newID())
			}
		} else if err != nil {
			return err
		}
		if a.ID != "" && a.ID != id {
			return repository.ErrConflict
		}
		now := nowMillis()
		_, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,provider,provider_account_id,address,display_name,credential_ref,created_at,updated_at)
   VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider,provider_account_id) DO UPDATE SET
   address=excluded.address,display_name=excluded.display_name,credential_ref=excluded.credential_ref,updated_at=excluded.updated_at`,
			id, a.Provider, a.ProviderAccountID, a.Address.Address, a.Address.Name, optionalString(a.CredentialRef), now, now)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}
func (r *accountRepository) Get(ctx context.Context, id domain.AccountID) (domain.Account, error) {
	var a domain.Account
	var created, updated int64
	var ref sql.NullString
	err := r.store.db.QueryRowContext(ctx, `SELECT id,provider,provider_account_id,address,display_name,credential_ref,created_at,updated_at FROM accounts WHERE id=?`, id).
		Scan(&a.ID, &a.Provider, &a.ProviderAccountID, &a.Address.Address, &a.Address.Name, &ref, &created, &updated)
	if err != nil {
		return domain.Account{}, storageError(err)
	}
	a.CredentialRef = ref.String
	a.CreatedAt = instant(created)
	a.UpdatedAt = instant(updated)
	return a, nil
}
