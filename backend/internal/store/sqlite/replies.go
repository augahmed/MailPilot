package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

type replyRepository struct{ store *Store }

func (s *Store) Replies() reply.Store { return &replyRepository{s} }
func (r *replyRepository) Sources(ctx context.Context, a domain.AccountID, id domain.EmailID, thread bool) ([]repository.StoredEmail, bool, error) {
	selected, err := r.store.Emails().Get(ctx, a, id)
	if err != nil {
		return nil, false, err
	}
	var result []repository.StoredEmail
	capped := false
	if thread && selected.Email.ProviderThreadID != "" {
		rows, e := r.store.db.QueryContext(ctx, `SELECT id FROM emails WHERE account_id=? AND provider_thread_id=? AND received_at<=? AND id<>? ORDER BY received_at DESC,id DESC LIMIT 5`, a, selected.Email.ProviderThreadID, selected.Email.ReceivedAt.UnixMilli(), id)
		if e != nil {
			return nil, false, repository.ErrStorage
		}
		var ids []domain.EmailID
		for rows.Next() {
			var other domain.EmailID
			if e = rows.Scan(&other); e != nil {
				break
			}
			ids = append(ids, other)
		}
		scanErr := rows.Err()
		_ = rows.Close()
		if e != nil || scanErr != nil {
			return nil, false, repository.ErrStorage
		}
		capped = len(ids) > 4
		ids = ids[:min(4, len(ids))]
		for i := len(ids) - 1; i >= 0; i-- {
			source, e := r.store.Emails().Get(ctx, a, ids[i])
			if e != nil {
				return nil, false, e
			}
			result = append(result, source)
		}
	}
	return append(result, selected), capped, nil
}
func (r *replyRepository) Settings(ctx context.Context, a domain.AccountID) (reply.Settings, error) {
	var s reply.Settings
	e := r.store.db.QueryRowContext(ctx, `SELECT model,enabled FROM reply_ai_settings WHERE account_id=?`, a).Scan(&s.Model, &s.Enabled)
	if errors.Is(e, sql.ErrNoRows) {
		return s, nil
	}
	if e != nil {
		return s, repository.ErrStorage
	}
	return s, nil
}
func (r *replyRepository) SaveSettings(ctx context.Context, a domain.AccountID, s reply.Settings) error {
	if a == "" || len(s.Model) > 128 || s.Enabled && s.Model == "" {
		return repository.ErrInvalid
	}
	_, e := r.store.db.ExecContext(ctx, `INSERT INTO reply_ai_settings VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET model=excluded.model,enabled=excluded.enabled`, a, s.Model, s.Enabled)
	if e != nil {
		return repository.ErrStorage
	}
	return nil
}
func (r *replyRepository) Create(ctx context.Context, p reply.Plan) error {
	if !p.Valid() || !time.Now().Before(p.ExpiresAt) || p.ExpiresAt.After(time.Now().Add(6*time.Minute)) {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		var count int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM reply_draft_actions WHERE account_id=? AND state IN ('in_flight','uncertain','conflict')`, p.AccountID).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return repository.ErrConflict
		}
		for _, src := range p.Sources {
			var hash string
			if e := tx.QueryRowContext(ctx, `SELECT content_hash FROM emails WHERE account_id=? AND id=?`, p.AccountID, src.ID).Scan(&hash); e != nil {
				return repository.ErrNotFound
			}
			if hash != src.Hash {
				return repository.ErrConflict
			}
		}
		raw, e := json.Marshal(p)
		if e != nil {
			return repository.ErrInvalid
		}
		var created int64
		if e = tx.QueryRowContext(ctx, `SELECT max(?,coalesce(max(created_at)+1,?)) FROM reply_draft_actions WHERE account_id=?`, nowMillis(), nowMillis(), p.AccountID).Scan(&created); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO reply_draft_actions(account_id,id,email_id,plan_json,state,created_at,updated_at) VALUES(?,?,?,?,'preview',?,?)`, p.AccountID, p.ID, p.EmailID, string(raw), created, created)
		if e != nil {
			return e
		}
		for _, src := range p.Sources {
			if _, e = tx.ExecContext(ctx, `INSERT INTO reply_draft_sources VALUES(?,?,?)`, p.AccountID, p.ID, src.ID); e != nil {
				return e
			}
		}
		return nil
	})
}
func scanDraft(row scanner) (reply.Action, error) {
	var a reply.Action
	var plan, ref string
	e := row.Scan(&plan, &a.State, &ref, &a.Code, &a.Reconciled)
	if errors.Is(e, sql.ErrNoRows) {
		return a, repository.ErrNotFound
	}
	if e != nil {
		return a, repository.ErrStorage
	}
	if json.Unmarshal([]byte(plan), &a.Plan) != nil || json.Unmarshal([]byte(ref), &a.Reference) != nil || !a.Plan.Valid() {
		return reply.Action{}, repository.ErrStorage
	}
	return a, nil
}
func (r *replyRepository) Get(ctx context.Context, a domain.AccountID, id string) (reply.Action, error) {
	return scanDraft(r.store.db.QueryRowContext(ctx, `SELECT plan_json,state,reference_json,code,reconciled FROM reply_draft_actions WHERE account_id=? AND id=?`, a, id))
}
func (r *replyRepository) Latest(ctx context.Context, a domain.AccountID) (reply.Action, error) {
	return scanDraft(r.store.db.QueryRowContext(ctx, `SELECT plan_json,state,reference_json,code,reconciled FROM reply_draft_actions WHERE account_id=? ORDER BY CASE WHEN state IN ('in_flight','uncertain','conflict') THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 1`, a))
}
func (r *replyRepository) Transition(ctx context.Context, a domain.AccountID, id, from, to string, ref provider.DraftReference, code string, reconciled bool) error {
	valid := from == "preview" && to == "in_flight" || from == "in_flight" && (to == "created" || to == "uncertain" || to == "conflict") || (from == "uncertain" || from == "conflict") && (to == "created" || to == "uncertain" || to == "conflict" || to == "resolved") || from == "in_flight" && to == "resolved"
	if !valid || len(code) > 64 || to == "created" && (ref.AccountID != a || ref.ProviderDraftID == "" || ref.ProviderMessageID == "") {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if to == "in_flight" {
			action, e := scanDraft(tx.QueryRowContext(ctx, `SELECT plan_json,state,reference_json,code,reconciled FROM reply_draft_actions WHERE account_id=? AND id=?`, a, id))
			if e != nil {
				return e
			}
			if !time.Now().Before(action.Plan.ExpiresAt) {
				return repository.ErrConflict
			}
			for _, src := range action.Plan.Sources {
				var h string
				if e = tx.QueryRowContext(ctx, `SELECT content_hash FROM emails WHERE account_id=? AND id=?`, a, src.ID).Scan(&h); e != nil || h != src.Hash {
					return repository.ErrConflict
				}
			}
		}
		raw, _ := json.Marshal(ref)
		query := `UPDATE reply_draft_actions SET state=?,reference_json=?,code=?,reconciled=?,updated_at=? WHERE account_id=? AND id=? AND state=?`
		if to == "resolved" {
			query += " AND reconciled=1"
		}
		result, e := tx.ExecContext(ctx, query, to, string(raw), code, reconciled, nowMillis(), a, id, from)
		if e != nil {
			return repository.ErrConflict
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return repository.ErrConflict
		}
		return nil
	})
}
