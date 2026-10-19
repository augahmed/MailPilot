package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
)

type replyRepository struct{ store *Store }

func (s *Store) Replies() reply.Store { return &replyRepository{s} }
func scanDraft(row scanner) (reply.Action, error) {
	var a reply.Action
	var plan, ref string
	e := row.Scan(&plan, &a.State, &ref, &a.Code, &a.Reconciled)
	if errors.Is(e, sql.ErrNoRows) {
		return a, repository.ErrNotFound
	}
	if e != nil {
		return a, storageError(e)
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
	valid := from == "in_flight" && (to == "created" || to == "uncertain" || to == "conflict") || (from == "uncertain" || from == "conflict") && (to == "created" || to == "uncertain" || to == "conflict" || to == "resolved") || from == "in_flight" && to == "resolved"
	if !valid || len(code) > 64 || to == "created" && (ref.AccountID != a || ref.ProviderDraftID == "" || ref.ProviderMessageID == "") {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
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
