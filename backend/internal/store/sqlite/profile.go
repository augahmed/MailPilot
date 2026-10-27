package sqlite

import (
	"context"
	"errors"
)

// VerifyProfile fences accidentally copied/misrouted profiles before any mailbox
// service is constructed. It never reads message content or changes journals.
func (s *Store) VerifyProfile(ctx context.Context, demo bool) error {
	s.demo = false
	var incompatible int
	query := `SELECT count(*) FROM accounts WHERE id='mailpilot-synthetic-demo-v1' OR provider='synthetic'`
	if demo {
		query = `SELECT count(*) FROM accounts WHERE id<>'mailpilot-synthetic-demo-v1' OR provider<>'synthetic' OR coalesce(credential_ref,'')<>''`
	}
	if err := s.db.QueryRowContext(ctx, query).Scan(&incompatible); err != nil {
		return err
	}
	if incompatible != 0 {
		return errors.New("incompatible application profile")
	}
	s.demo = demo
	return nil
}
