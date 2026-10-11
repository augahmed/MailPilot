package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"mailpilot.local/core/internal/repository"
	driver "modernc.org/sqlite"
)

type emailRepository struct{ store *Store }
type accountRepository struct{ store *Store }
type categoryRepository struct{ store *Store }
type actionRepository struct{ store *Store }

func (s *Store) Emails() repository.EmailRepository                 { return &emailRepository{s} }
func (s *Store) Accounts() repository.AccountRepository             { return &accountRepository{s} }
func (s *Store) Categories() repository.CategoryRepository          { return &categoryRepository{s} }
func (s *Store) MailboxActions() repository.MailboxActionRepository { return &actionRepository{s} }

func newID() string              { return rand.Text() }
func nowMillis() int64           { return time.Now().UTC().UnixMilli() }
func instant(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
func optionalTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := instant(value.Int64)
	return &t
}
func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Never expose driver errors, SQL arguments, or email content to callers/logs.
func storageError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, repository.ErrInvalid, repository.ErrConflict, repository.ErrNotFound, repository.ErrStorage} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return repository.ErrNotFound
	}
	var sqliteErr *driver.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 19, 5, 6:
			return repository.ErrConflict
		}
	}
	return repository.ErrStorage
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit())
}

func exists(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	var one int
	return tx.QueryRowContext(ctx, query, args...).Scan(&one)
}
