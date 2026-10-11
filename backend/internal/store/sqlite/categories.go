package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func (r *categoryRepository) Save(ctx context.Context, a domain.AccountID, c domain.EmailCategory) (domain.CategoryID, error) {
	c.Name = strings.TrimSpace(c.Name)
	if a == "" || c.Name == "" {
		return "", repository.ErrInvalid
	}
	id := c.ID
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if id == "" {
			err := tx.QueryRowContext(ctx, "SELECT id FROM categories WHERE account_id=? AND name=?", a, c.Name).Scan(&id)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			id = domain.CategoryID(newID())
		} else {
			result, err := tx.ExecContext(ctx, "UPDATE categories SET name=? WHERE account_id=? AND id=?", c.Name, a, id)
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
		_, err := tx.ExecContext(ctx, "INSERT INTO categories VALUES(?,?,?)", id, a, c.Name)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}
func (r *categoryRepository) Get(ctx context.Context, a domain.AccountID, id domain.CategoryID) (domain.EmailCategory, error) {
	var c domain.EmailCategory
	err := r.store.db.QueryRowContext(ctx, "SELECT id,name FROM categories WHERE account_id=? AND id=?", a, id).Scan(&c.ID, &c.Name)
	return c, storageError(err)
}
func (r *categoryRepository) List(ctx context.Context, a domain.AccountID) ([]domain.EmailCategory, error) {
	if a == "" {
		return nil, repository.ErrInvalid
	}
	rows, err := r.store.db.QueryContext(ctx, "SELECT id,name FROM categories WHERE account_id=? ORDER BY name,id", a)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []domain.EmailCategory{}
	for rows.Next() {
		var c domain.EmailCategory
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
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
		if err := exists(ctx, tx, "SELECT 1 FROM emails WHERE account_id=? AND id=?", a, email); err != nil {
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
func (r *categoryRepository) SaveClassification(ctx context.Context, c domain.ClassificationResult) error {
	if c.AccountID == "" || c.EmailID == "" || c.CategoryID == "" || c.Confidence < 0 || c.Confidence > 1 || math.IsNaN(c.Confidence) || c.ClassifierVersion == "" || c.InputHash == "" || c.AssessedAt.IsZero() {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkInputHash(ctx, tx, c.AccountID, c.EmailID, c.InputHash); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO classification_results VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(email_id,category_id) DO UPDATE SET
   confidence=excluded.confidence,classifier_version=excluded.classifier_version,input_hash=excluded.input_hash,explanation=excluded.explanation,assessed_at=excluded.assessed_at
   WHERE excluded.assessed_at >= classification_results.assessed_at`, c.AccountID, c.EmailID, c.CategoryID, c.Confidence, c.ClassifierVersion, c.InputHash, c.Explanation, c.AssessedAt.UnixMilli())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return repository.ErrConflict
		}
		return err
	})
}
func (r *categoryRepository) ListClassifications(ctx context.Context, a domain.AccountID, id domain.EmailID) ([]domain.ClassificationResult, error) {
	if a == "" || id == "" {
		return nil, repository.ErrInvalid
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT account_id,email_id,category_id,confidence,classifier_version,input_hash,explanation,assessed_at FROM classification_results WHERE account_id=? AND email_id=? ORDER BY confidence DESC,category_id`, a, id)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []domain.ClassificationResult{}
	for rows.Next() {
		var c domain.ClassificationResult
		var assessed int64
		if err := rows.Scan(&c.AccountID, &c.EmailID, &c.CategoryID, &c.Confidence, &c.ClassifierVersion, &c.InputHash, &c.Explanation, &assessed); err != nil {
			return nil, storageError(err)
		}
		c.AssessedAt = instant(assessed)
		result = append(result, c)
	}
	return result, storageError(rows.Err())
}
