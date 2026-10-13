package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func (r *categoryRepository) Save(ctx context.Context, a domain.AccountID, c domain.EmailCategory) (domain.CategoryID, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if a == "" || c.Name == "" || len(c.Name) > 100 || c.Description == "" || len(c.Description) > 1000 {
		return "", repository.ErrInvalid
	}
	id := c.ID
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		now := nowMillis()
		if id == "" {
			id = domain.CategoryID(newID())
		} else {
			result, err := tx.ExecContext(ctx, "UPDATE categories SET name=?,description=?,enabled=?,updated_at=max(updated_at+1,?) WHERE account_id=? AND id=?", c.Name, c.Description, c.Enabled, now, a, id)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 {
				return nil
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", id, a, c.Name, c.Description, c.Enabled, now, now)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}
func (r *categoryRepository) Get(ctx context.Context, a domain.AccountID, id domain.CategoryID) (domain.EmailCategory, error) {
	c, err := scanCategory(r.store.db.QueryRowContext(ctx, "SELECT id,name,description,enabled,created_at,updated_at FROM categories WHERE account_id=? AND id=?", a, id))
	return c, storageError(err)
}
func (r *categoryRepository) List(ctx context.Context, a domain.AccountID) ([]domain.EmailCategory, error) {
	if a == "" {
		return nil, repository.ErrInvalid
	}
	rows, err := r.store.db.QueryContext(ctx, "SELECT id,name,description,enabled,created_at,updated_at FROM categories WHERE account_id=? ORDER BY name,id", a)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []domain.EmailCategory{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, storageError(err)
		}
		result = append(result, c)
	}
	return result, storageError(rows.Err())
}
func (r *categoryRepository) Assign(ctx context.Context, a domain.AccountID, email domain.EmailID, category domain.CategoryID) error {
	if a == "" || email == "" || category == "" {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, "SELECT 1 FROM emails WHERE remote_deleted=0 AND account_id=? AND id=?", a, email); err != nil {
			return err
		}
		if err := exists(ctx, tx, "SELECT 1 FROM categories WHERE account_id=? AND id=?", a, category); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO email_categories VALUES(?,?,?) ON CONFLICT DO NOTHING", a, email, category)
		return err
	})
}
func (r *categoryRepository) Unassign(ctx context.Context, a domain.AccountID, email domain.EmailID, category domain.CategoryID) error {
	if a == "" || email == "" || category == "" {
		return repository.ErrInvalid
	}
	_, err := r.store.db.ExecContext(ctx, "DELETE FROM email_categories WHERE account_id=? AND email_id=? AND category_id=?", a, email, category)
	return storageError(err)
}
func scanCategory(row interface{ Scan(...any) error }) (domain.EmailCategory, error) {
	var c domain.EmailCategory
	var created, updated int64
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Enabled, &created, &updated)
	c.CreatedAt = instant(created)
	c.UpdatedAt = instant(updated)
	return c, err
}
func (r *categoryRepository) Delete(ctx context.Context, a domain.AccountID, id domain.CategoryID) error {
	if a == "" || id == "" {
		return repository.ErrInvalid
	}
	result, err := r.store.db.ExecContext(ctx, "DELETE FROM categories WHERE account_id=? AND id=?", a, id)
	if err != nil {
		return storageError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return storageError(err)
	}
	if n == 0 {
		return repository.ErrNotFound
	}
	return nil
}
