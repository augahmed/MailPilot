package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func labelSet(ids []domain.ProviderLabelID) ([]domain.ProviderLabelID, error) {
	result := append([]domain.ProviderLabelID{}, ids...)
	slices.Sort(result)
	for i, id := range result {
		if id == "" || (i > 0 && result[i-1] == id) {
			return nil, repository.ErrInvalid
		}
	}
	return result, nil
}
func normalizeDelta(delta domain.LabelDelta) (domain.LabelDelta, error) {
	var err error
	delta.Add, err = labelSet(delta.Add)
	if err != nil {
		return delta, err
	}
	delta.Remove, err = labelSet(delta.Remove)
	if err != nil {
		return delta, err
	}
	for _, id := range delta.Add {
		if slices.Contains(delta.Remove, id) {
			return delta, repository.ErrInvalid
		}
	}
	return delta, nil
}
func encode(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", repository.ErrInvalid
	}
	return string(data), nil
}

func readAction(ctx context.Context, tx *sql.Tx, account domain.AccountID, id domain.ActionID) (domain.MailboxAction, error) {
	var a domain.MailboxAction
	var created, expires int64
	var confirmed, completed sql.NullInt64
	var reverses sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,account_id,kind,reverses_action_id,status,plan_digest,revision,created_at,expires_at,confirmed_at,completed_at FROM mailbox_actions WHERE account_id=? AND id=?`, account, id).
		Scan(&a.ID, &a.AccountID, &a.Kind, &reverses, &a.Status, &a.PlanDigest, &a.Revision, &created, &expires, &confirmed, &completed)
	if err != nil {
		return a, err
	}
	a.CreatedAt = instant(created)
	a.ExpiresAt = instant(expires)
	a.ConfirmedAt = optionalTime(confirmed)
	a.CompletedAt = optionalTime(completed)
	if reverses.Valid {
		value := domain.ActionID(reverses.String)
		a.ReversesActionID = &value
	}
	rows, err := tx.QueryContext(ctx, `SELECT email_id,provider_message_id,requested_delta_json,before_labels_json,actual_delta_json,after_labels_json,state,attempt_count,last_error_code,updated_at FROM mailbox_action_items WHERE account_id=? AND action_id=? ORDER BY email_id`, account, id)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.MailboxActionItem
		var requested, before string
		var actual, after sql.NullString
		var updated int64
		if err := rows.Scan(&item.EmailID, &item.ProviderMessageID, &requested, &before, &actual, &after, &item.State, &item.AttemptCount, &item.LastErrorCode, &updated); err != nil {
			return a, err
		}
		if err := json.Unmarshal([]byte(requested), &item.RequestedDelta); err != nil {
			return a, err
		}
		if err := json.Unmarshal([]byte(before), &item.BeforeLabels); err != nil {
			return a, err
		}
		if actual.Valid {
			if err := json.Unmarshal([]byte(actual.String), &item.ActualDelta); err != nil {
				return a, err
			}
		}
		if after.Valid {
			if err := json.Unmarshal([]byte(after.String), &item.AfterLabels); err != nil {
				return a, err
			}
		}
		item.UpdatedAt = instant(updated)
		a.Items = append(a.Items, item)
	}
	return a, rows.Err()
}
func (r *actionRepository) Get(ctx context.Context, a domain.AccountID, id domain.ActionID) (domain.MailboxAction, error) {
	var result domain.MailboxAction
	err := r.store.transaction(ctx, func(tx *sql.Tx) error { var err error; result, err = readAction(ctx, tx, a, id); return err })
	if err != nil {
		return domain.MailboxAction{}, err
	}
	return result, nil
}
func (r *actionRepository) ListRecoverable(ctx context.Context, a domain.AccountID, page repository.Page) ([]domain.MailboxAction, error) {
	if a == "" || page.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	result := []domain.MailboxAction{}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM mailbox_actions WHERE account_id=? AND status IN ('applying','uncertain') ORDER BY created_at,id LIMIT ? OFFSET ?", a, page.Limit, page.Offset)
		if err != nil {
			return err
		}
		ids := []domain.ActionID{}
		for rows.Next() {
			var id domain.ActionID
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			action, err := readAction(ctx, tx, a, id)
			if err != nil {
				return err
			}
			result = append(result, action)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func allowedTransition(a domain.MailboxAction, to domain.ActionStatus, now int64) bool {
	if a.Status == domain.ActionPreview {
		switch to {
		case domain.ActionApplying:
			return now < a.ExpiresAt.UnixMilli()
		case domain.ActionCancelled:
			return true
		case domain.ActionExpired:
			return now >= a.ExpiresAt.UnixMilli()
		}
		return false
	}
	if a.Status != domain.ActionApplying && a.Status != domain.ActionUncertain {
		return false
	}
	if to == domain.ActionApplying {
		return a.Status == domain.ActionUncertain
	}
	applied, unresolved, pending, other := 0, 0, 0, 0
	for _, item := range a.Items {
		switch item.State {
		case domain.ItemApplied:
			applied++
		case domain.ItemInFlight, domain.ItemUncertain:
			unresolved++
		case domain.ItemPending:
			pending++
		case domain.ItemSkipped:
		default:
			other++
		}
	}
	if to == domain.ActionUncertain {
		return unresolved > 0
	}
	if unresolved > 0 {
		return false
	}
	switch to {
	case domain.ActionApplied:
		return pending == 0 && other == 0
	case domain.ActionPartial:
		return pending == 0 && applied > 0 && other > 0
	case domain.ActionFailed:
		return pending == 0 && applied == 0 && other > 0
	case domain.ActionCancelled:
		return true
	}
	return false
}
func (r *actionRepository) Transition(ctx context.Context, a domain.AccountID, id domain.ActionID, revision int64, to domain.ActionStatus) error {
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		action, err := readAction(ctx, tx, a, id)
		if err != nil {
			return err
		}
		now := nowMillis()
		if action.Revision != revision {
			return repository.ErrConflict
		}
		if !allowedTransition(action, to, now) {
			return repository.ErrInvalid
		}
		var confirmed, completed any
		if action.ConfirmedAt != nil {
			confirmed = action.ConfirmedAt.UnixMilli()
		} else if to == domain.ActionApplying {
			confirmed = now
		}
		if to != domain.ActionApplying && to != domain.ActionUncertain {
			completed = now
		}
		_, err = tx.ExecContext(ctx, "UPDATE mailbox_actions SET status=?,revision=revision+1,confirmed_at=?,completed_at=? WHERE account_id=? AND id=? AND revision=?", to, confirmed, completed, a, id, revision)
		return err
	})
}

func allowedItemTransition(from, to domain.ActionItemState) bool {
	switch from {
	case domain.ItemPending:
		return to == domain.ItemInFlight || to == domain.ItemSkipped || to == domain.ItemConflict
	case domain.ItemInFlight:
		return to == domain.ItemApplied || to == domain.ItemFailed || to == domain.ItemUncertain
	case domain.ItemUncertain:
		return to == domain.ItemApplied || to == domain.ItemFailed || to == domain.ItemConflict
	}
	return false
}
func validErrorCode(code string) bool {
	if len(code) > 64 {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
func validateOutcome(item domain.MailboxActionItem, update repository.ActionItemUpdate) (*domain.LabelDelta, *[]domain.ProviderLabelID, error) {
	if update.State != domain.ItemApplied {
		if update.ActualDelta != nil || update.AfterLabels != nil {
			return nil, nil, repository.ErrInvalid
		}
		return nil, nil, nil
	}
	if update.ActualDelta == nil || update.AfterLabels == nil {
		return nil, nil, repository.ErrInvalid
	}
	actual, err := normalizeDelta(*update.ActualDelta)
	if err != nil {
		return nil, nil, err
	}
	after, err := labelSet(*update.AfterLabels)
	if err != nil {
		return nil, nil, err
	}
	desired := append([]domain.ProviderLabelID{}, item.BeforeLabels...)
	for _, id := range item.RequestedDelta.Remove {
		desired = slices.DeleteFunc(desired, func(x domain.ProviderLabelID) bool { return x == id })
	}
	for _, id := range item.RequestedDelta.Add {
		if !slices.Contains(desired, id) {
			desired = append(desired, id)
		}
	}
	slices.Sort(desired)
	derived := domain.LabelDelta{Add: []domain.ProviderLabelID{}, Remove: []domain.ProviderLabelID{}}
	for _, id := range after {
		if !slices.Contains(item.BeforeLabels, id) {
			derived.Add = append(derived.Add, id)
		}
	}
	for _, id := range item.BeforeLabels {
		if !slices.Contains(after, id) {
			derived.Remove = append(derived.Remove, id)
		}
	}
	if !slices.Equal(desired, after) || (!slices.Equal(derived.Add, actual.Add) || !slices.Equal(derived.Remove, actual.Remove)) {
		return nil, nil, repository.ErrInvalid
	}
	return &actual, &after, nil
}
func (r *actionRepository) UpdateItem(ctx context.Context, a domain.AccountID, id domain.ActionID, revision int64, u repository.ActionItemUpdate) error {
	if !validErrorCode(u.LastErrorCode) {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		action, err := readAction(ctx, tx, a, id)
		if err != nil {
			return err
		}
		if action.Revision != revision {
			return repository.ErrConflict
		}
		if action.Status != domain.ActionApplying && action.Status != domain.ActionUncertain {
			return repository.ErrInvalid
		}
		var item *domain.MailboxActionItem
		for i := range action.Items {
			if action.Items[i].EmailID == u.EmailID {
				item = &action.Items[i]
				break
			}
		}
		if item == nil {
			return repository.ErrNotFound
		}
		if !allowedItemTransition(item.State, u.State) {
			return repository.ErrInvalid
		}
		actual, after, err := validateOutcome(*item, u)
		if err != nil {
			return err
		}
		var actualJSON, afterJSON any
		if actual != nil {
			actualJSON, err = encode(actual)
			if err != nil {
				return err
			}
		}
		if after != nil {
			afterJSON, err = encode(after)
			if err != nil {
				return err
			}
		}
		attempts := item.AttemptCount
		if u.State == domain.ItemInFlight {
			attempts++
		}
		_, err = tx.ExecContext(ctx, `UPDATE mailbox_action_items SET state=?,actual_delta_json=?,after_labels_json=?,attempt_count=?,last_error_code=?,updated_at=? WHERE account_id=? AND action_id=? AND email_id=?`, u.State, actualJSON, afterJSON, attempts, u.LastErrorCode, nowMillis(), a, id, u.EmailID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE mailbox_actions SET revision=revision+1 WHERE account_id=? AND id=? AND revision=?", a, id, revision)
		return err
	})
}
