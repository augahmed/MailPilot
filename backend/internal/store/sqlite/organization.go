package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type organizationRepository struct{ store *Store }

func (s *Store) Organization() repository.OrganizationRepository { return &organizationRepository{s} }
func (r *organizationRepository) Create(ctx context.Context, p domain.OrganizationPlan) (domain.OrganizationPlan, error) {
	if p.AccountID == "" || len(p.Rows) == 0 || len(p.Rows) > 5 || len(p.Steps) > 10 || (p.Kind != domain.ActionLabelDelta && p.Kind != domain.ActionUndo) || (p.Kind == domain.ActionUndo) != (p.Reverses != "") {
		return p, repository.ErrInvalid
	}
	p.ID = domain.ActionID(newID())
	p.Status = domain.ActionPreview
	p.Revision = 0
	p.CreatedAt = nowMillis()
	p.ExpiresAt = time.Now().Add(5 * time.Minute).UnixMilli()
	p.Digest = ""
	seen := map[domain.EmailID]bool{}
	for _, row := range p.Rows {
		if row.EmailID == "" || row.ProviderID == "" || len(row.ProposedLabel) > 110 || math.IsNaN(row.Confidence) || math.IsInf(row.Confidence, 0) || row.Confidence < 0 || row.Confidence > 1 || seen[row.EmailID] || !validOrganizationEvidence(row) {
			return p, repository.ErrInvalid
		}
		seen[row.EmailID] = true
	}
	for _, step := range p.Steps {
		matched := false
		for _, row := range p.Rows {
			if step.Name == row.ProposedLabel && (step.Kind == "create_label" || step.EmailID == row.EmailID) {
				matched = true
			}
		}
		if !matched {
			return p, repository.ErrInvalid
		}
		if step.State != domain.ItemPending || step.ResultID != "" || step.Added || len(step.AfterLabels) != 0 || step.Code != "" || (step.Kind != "create_label" && step.Kind != "add" && step.Kind != "remove") || (!strings.HasPrefix(step.Name, "MailPilot/") && step.Name != domain.PotentialPhishingLabel) || (step.Kind != "create_label" && !seen[step.EmailID]) || (step.Kind == "remove" && p.Kind != domain.ActionUndo) {
			return p, repository.ErrInvalid
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return p, repository.ErrInvalid
	}
	sum := sha256.Sum256(data)
	p.Digest = hex.EncodeToString(sum[:])
	plan, err := encode(p)
	if err != nil {
		return p, err
	}
	steps, err := encode(p.Steps)
	if err != nil {
		return p, err
	}
	err = r.store.transaction(ctx, func(tx *sql.Tx) error {
		for _, row := range p.Rows {
			if err := exists(ctx, tx, "SELECT 1 FROM emails WHERE content_hash<>'' AND account_id=? AND id=? AND provider_message_id=?", p.AccountID, row.EmailID, row.ProviderID); err != nil {
				return err
			}
		}
		var reverse any
		if p.Reverses != "" {
			reverse = string(p.Reverses)
			if err := exists(ctx, tx, `SELECT 1 FROM mailbox_actions WHERE account_id=? AND id=? AND kind='label_delta' AND status IN ('applied','partial','failed','cancelled')`, p.AccountID, p.Reverses); err != nil {
				return err
			}
			if err := exists(ctx, tx, `SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM mailbox_actions WHERE account_id=? AND reverses_action_id=? AND status NOT IN ('preview','expired','cancelled'))`, p.AccountID, p.Reverses); err != nil {
				return repository.ErrConflict
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO mailbox_actions(id,account_id,kind,reverses_action_id,status,plan_digest,revision,created_at,expires_at) VALUES(?,?,?,?,?,?,0,?,?)`, p.ID, p.AccountID, p.Kind, reverse, p.Status, p.Digest, p.CreatedAt, p.ExpiresAt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO organization_plans VALUES(?,?,?,?)`, p.AccountID, p.ID, plan, steps)
		if err != nil {
			return err
		}
		for _, row := range p.Rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO organization_emails VALUES(?,?,?,?)`, p.AccountID, p.ID, row.EmailID, optionalString(string(row.CategoryID))); err != nil {
				return err
			}
		}
		return nil
	})
	return p, err
}
func readOrganization(ctx context.Context, tx *sql.Tx, a domain.AccountID, id domain.ActionID) (domain.OrganizationPlan, error) {
	var p domain.OrganizationPlan
	var data, steps string
	var status domain.ActionStatus
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT o.plan_json,o.steps_json,a.status,a.revision FROM organization_plans o JOIN mailbox_actions a ON a.account_id=o.account_id AND a.id=o.action_id WHERE o.account_id=? AND o.action_id=?`, a, id).Scan(&data, &steps, &status, &revision)
	if err != nil {
		return p, err
	}
	if json.Unmarshal([]byte(data), &p) != nil || json.Unmarshal([]byte(steps), &p.Steps) != nil {
		return p, repository.ErrStorage
	}
	p.Status = status
	p.Revision = revision
	return p, nil
}
func (r *organizationRepository) Get(ctx context.Context, a domain.AccountID, id domain.ActionID) (domain.OrganizationPlan, error) {
	var p domain.OrganizationPlan
	err := r.store.transaction(ctx, func(tx *sql.Tx) error { var err error; p, err = readOrganization(ctx, tx, a, id); return err })
	return p, err
}
func (r *organizationRepository) Latest(ctx context.Context, a domain.AccountID, undo bool) (domain.OrganizationPlan, error) {
	var p domain.OrganizationPlan
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		var id domain.ActionID
		query := `SELECT a.id FROM mailbox_actions a JOIN organization_plans o ON o.account_id=a.account_id AND o.action_id=a.id WHERE a.account_id=?`
		if undo {
			query += ` AND a.kind='label_delta' AND a.confirmed_at IS NOT NULL AND a.status IN ('applied','partial','failed','cancelled')`
		}
		// Recovery always precedes newer previews; never hide unfinished writes.
		if undo {
			query += ` ORDER BY a.confirmed_at DESC,a.created_at DESC,a.id DESC LIMIT 1`
		} else {
			query += ` ORDER BY (a.status IN ('applying','uncertain')) DESC,a.created_at DESC,a.id DESC LIMIT 1`
		}
		if err := tx.QueryRowContext(ctx, query, a).Scan(&id); err != nil {
			return err
		}
		var err error
		p, err = readOrganization(ctx, tx, a, id)
		return err
	})
	return p, err
}
func (r *organizationRepository) Update(ctx context.Context, p domain.OrganizationPlan) (domain.OrganizationPlan, error) {
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		old, err := readOrganization(ctx, tx, p.AccountID, p.ID)
		if err != nil {
			return err
		}
		if len(p.Steps) != len(old.Steps) {
			return repository.ErrInvalid
		}
		if old.Revision != p.Revision {
			return repository.ErrConflict
		}
		immutable := p
		immutable.Status = old.Status
		immutable.Steps = old.Steps
		if !reflect.DeepEqual(immutable, old) {
			return repository.ErrInvalid
		}
		if old.Status == domain.ActionPreview {
			if p.Status != domain.ActionApplying && p.Status != domain.ActionCancelled {
				return repository.ErrInvalid
			}
			if p.Status == domain.ActionApplying && nowMillis() >= p.ExpiresAt {
				return repository.ErrConflict
			}
			if !reflect.DeepEqual(old.Steps, p.Steps) {
				return repository.ErrInvalid
			}
			if p.Kind == domain.ActionUndo && p.Status == domain.ActionApplying {
				if err := exists(ctx, tx, `SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM mailbox_actions WHERE account_id=? AND reverses_action_id=? AND id<>? AND confirmed_at IS NOT NULL)`, p.AccountID, p.Reverses, p.ID); err != nil {
					return repository.ErrConflict
				}
			}
		} else if old.Status != domain.ActionApplying && old.Status != domain.ActionUncertain {
			return repository.ErrInvalid
		}
		unresolved := false
		pending := false
		applied := 0
		other := 0
		for i, s := range p.Steps {
			prior := old.Steps[i]
			fixed := s
			fixed.State = prior.State
			fixed.ResultID = prior.ResultID
			fixed.Added = prior.Added
			fixed.AfterLabels = prior.AfterLabels
			fixed.Code = prior.Code
			if !reflect.DeepEqual(fixed, prior) || !validErrorCode(s.Code) {
				return repository.ErrInvalid
			}
			if !reflect.DeepEqual(s, prior) {
				if prior.State != s.State && !allowedItemTransition(prior.State, s.State) {
					return repository.ErrInvalid
				}
				if prior.State == s.State {
					return repository.ErrInvalid
				}
				if s.State == domain.ItemApplied {
					if s.ResultID == "" || (s.Added && s.Kind != "add") || (s.LabelID != "" && s.ResultID != s.LabelID) {
						return repository.ErrInvalid
					}
					if s.Kind != "create_label" {
						target := s.LabelID
						if target == "" {
							for _, creation := range p.Steps {
								if creation.Kind == "create_label" && creation.Name == s.Name && creation.State == domain.ItemApplied {
									target = creation.ResultID
								}
							}
						}
						if target == "" || target != s.ResultID || (s.Kind == "add") != slices.Contains(s.AfterLabels, target) {
							return repository.ErrInvalid
						}
						if s.Added {
							for _, row := range old.Rows {
								if row.EmailID == s.EmailID && slices.Contains(row.BeforeLabels, target) {
									return repository.ErrInvalid
								}
							}
						}
					}
				} else if s.ResultID != "" || s.Added || len(s.AfterLabels) != 0 {
					return repository.ErrInvalid
				}
			}
			switch s.State {
			case domain.ItemInFlight, domain.ItemUncertain:
				unresolved = true
			case domain.ItemPending:
				pending = true
			case domain.ItemApplied:
				applied++
			case domain.ItemSkipped:
			default:
				other++
			}
		}
		switch p.Status {
		case domain.ActionUncertain:
			if !unresolved {
				return repository.ErrInvalid
			}
		case domain.ActionApplied:
			if unresolved || pending || other > 0 {
				return repository.ErrInvalid
			}
		case domain.ActionPartial:
			if unresolved || pending || applied == 0 || other == 0 {
				return repository.ErrInvalid
			}
		case domain.ActionFailed:
			if unresolved || pending || other == 0 {
				return repository.ErrInvalid
			}
		case domain.ActionCancelled:
			if unresolved {
				return repository.ErrInvalid
			}
		case domain.ActionApplying:
		default:
			return repository.ErrInvalid
		}
		var confirmed, completed any
		if p.Status == domain.ActionApplying && old.Status == domain.ActionPreview {
			confirmed = nowMillis()
		}
		if p.Status != domain.ActionApplying && p.Status != domain.ActionUncertain && p.Status != domain.ActionPreview {
			completed = nowMillis()
		}
		_, err = tx.ExecContext(ctx, `UPDATE mailbox_actions SET status=?,revision=revision+1,confirmed_at=COALESCE(confirmed_at,?),completed_at=? WHERE account_id=? AND id=? AND revision=?`, p.Status, confirmed, completed, p.AccountID, p.ID, p.Revision)
		if err != nil {
			return err
		}
		steps, err := encode(p.Steps)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE organization_plans SET steps_json=? WHERE account_id=? AND action_id=?`, steps, p.AccountID, p.ID)
		return err
	})
	if err == nil {
		p.Revision++
	}
	return p, err
}

func validOrganizationEvidence(row domain.OrganizationRow) bool {
	if row.Phishing == nil {
		return row.CategoryID != "" && strings.HasPrefix(row.ProposedLabel, "MailPilot/")
	}
	p := row.Phishing
	if row.CategoryID != "" || row.ProposedLabel != domain.PotentialPhishingLabel || row.Confidence != 0 || p.Threshold < 31 || p.Threshold > 100 || p.Score < p.Threshold || p.Score > 100 || p.Version == "" || len(p.Version) > 200 || len(row.InputHash) != 64 || len(p.Findings) == 0 || len(p.Findings) > 12 {
		return false
	}
	for _, f := range p.Findings {
		if f.Points < -20 || f.Points > 100 || f.Code == "" || len(f.Code) > 100 || f.Explanation == "" || len(f.Explanation) > 300 {
			return false
		}
	}
	return true
}
