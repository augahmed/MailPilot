package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func classificationInputs(ctx context.Context, tx *sql.Tx, account domain.AccountID, id domain.EmailID) (domain.Email, []domain.EmailCategory, error) {
	stored, err := scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE account_id=? AND id=?", account, id))
	if err != nil {
		return domain.Email{}, nil, err
	}
	if err = loadEmailRelations(ctx, tx, &stored.Email); err != nil {
		return domain.Email{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,description,enabled,created_at,updated_at FROM categories WHERE account_id=? ORDER BY id", account)
	if err != nil {
		return domain.Email{}, nil, err
	}
	defer rows.Close()
	categories := []domain.EmailCategory{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return domain.Email{}, nil, err
		}
		categories = append(categories, c)
	}
	return stored.Email, categories, rows.Err()
}
func validClassificationRun(run domain.ClassificationRun) bool {
	if run.AccountID == "" || run.EmailID == "" || len(run.InputHash) != 64 || strings.TrimSpace(run.ClassifierName) == "" || len(run.ClassifierName) > 100 || strings.TrimSpace(run.ClassifierVersion) == "" || len(run.ClassifierVersion) > 100 || run.Timestamp.IsZero() || len(run.Results) > 1 || len(run.Candidates) > 5 || len(run.Reason) > 160 || !utf8.ValidString(run.Reason) || len(run.UsedClassifier) > 100 {
		return false
	}
	if run.Reason != "" && (strings.TrimSpace(run.Reason) == "" || strings.TrimSpace(run.UsedClassifier) == "" || !utf8.ValidString(run.UsedClassifier)) {
		return false
	}
	seen := map[domain.CategoryID]bool{}
	for i, candidate := range run.Candidates {
		if candidate.CategoryID == "" || seen[candidate.CategoryID] || math.IsNaN(candidate.Similarity) || math.IsInf(candidate.Similarity, 0) || candidate.Similarity < -1 || candidate.Similarity > 1 {
			return false
		}
		if i > 0 {
			previous := run.Candidates[i-1]
			if previous.Similarity < candidate.Similarity || (previous.Similarity == candidate.Similarity && previous.CategoryID > candidate.CategoryID) {
				return false
			}
		}
		seen[candidate.CategoryID] = true
	}
	seen = map[domain.CategoryID]bool{}
	for _, result := range run.Results {
		if result.AccountID != run.AccountID || result.EmailID != run.EmailID || result.CategoryID == "" || seen[result.CategoryID] || result.ClassifierName != run.ClassifierName || result.ClassifierVersion != run.ClassifierVersion || result.InputHash != run.InputHash || result.Timestamp.UnixMilli() != run.Timestamp.UnixMilli() || math.IsNaN(result.Confidence) || result.Confidence <= 0 || result.Confidence > 1 || len(result.Reasons) == 0 || len(result.Reasons) > 6 {
			return false
		}
		if result.ScoreKind != "" && result.ScoreKind != "rule_confidence" && result.ScoreKind != "cosine_similarity" {
			return false
		}
		if result.ScoreKind == "cosine_similarity" && (len(run.Candidates) == 0 || run.Candidates[0].CategoryID != result.CategoryID || run.Candidates[0].Similarity != result.Confidence) {
			return false
		}
		seen[result.CategoryID] = true
		for _, reason := range result.Reasons {
			if strings.TrimSpace(reason) == "" || len(reason) > 160 || !utf8.ValidString(reason) {
				return false
			}
		}
	}
	return true
}
func (r *classificationRepository) Replace(ctx context.Context, run domain.ClassificationRun) error {
	if !validClassificationRun(run) {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		email, categories, err := classificationInputs(ctx, tx, run.AccountID, run.EmailID)
		if err != nil {
			return err
		}
		if run.InputHash != classification.InputHash(email, categories) {
			return repository.ErrConflict
		}
		enabled := map[domain.CategoryID]bool{}
		for _, c := range categories {
			enabled[c.ID] = c.Enabled
		}
		for _, candidate := range run.Candidates {
			if !enabled[candidate.CategoryID] {
				return repository.ErrConflict
			}
		}
		for _, result := range run.Results {
			if !enabled[result.CategoryID] {
				return repository.ErrConflict
			}
		}
		var previous int64
		err = tx.QueryRowContext(ctx, "SELECT timestamp FROM classification_runs WHERE account_id=? AND email_id=?", run.AccountID, run.EmailID).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && previous > run.Timestamp.UnixMilli() {
			return repository.ErrConflict
		}
		candidates, err := json.Marshal(append([]domain.ClassificationCandidate{}, run.Candidates...))
		if err != nil {
			return repository.ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO classification_runs(account_id,email_id,classifier_name,classifier_version,input_hash,timestamp,used_classifier,reason,candidates_json) VALUES(?,?,?,?,?,?,?,?,?)
   ON CONFLICT(email_id) DO UPDATE SET classifier_name=excluded.classifier_name,classifier_version=excluded.classifier_version,input_hash=excluded.input_hash,timestamp=excluded.timestamp,used_classifier=excluded.used_classifier,reason=excluded.reason,candidates_json=excluded.candidates_json`, run.AccountID, run.EmailID, run.ClassifierName, run.ClassifierVersion, run.InputHash, run.Timestamp.UnixMilli(), run.UsedClassifier, run.Reason, string(candidates))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM classification_results WHERE account_id=? AND email_id=?", run.AccountID, run.EmailID); err != nil {
			return err
		}
		for _, result := range run.Results {
			reasons, err := json.Marshal(result.Reasons)
			if err != nil {
				return repository.ErrInvalid
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO classification_results(account_id,email_id,category_id,confidence,classifier_version,input_hash,explanation,assessed_at,classifier_name,reasons_json,score_kind) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, result.AccountID, result.EmailID, result.CategoryID, result.Confidence, result.ClassifierVersion, result.InputHash, strings.Join(result.Reasons, "\n"), result.Timestamp.UnixMilli(), result.ClassifierName, string(reasons), result.ScoreKind)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *classificationRepository) Get(ctx context.Context, account domain.AccountID, id domain.EmailID) (domain.ClassificationRun, error) {
	run := domain.ClassificationRun{AccountID: account, EmailID: id, Results: []domain.ClassificationResult{}}
	if account == "" || id == "" {
		return run, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		var timestamp int64
		var candidates string
		if err := tx.QueryRowContext(ctx, "SELECT classifier_name,classifier_version,input_hash,timestamp,used_classifier,reason,candidates_json FROM classification_runs WHERE account_id=? AND email_id=?", account, id).Scan(&run.ClassifierName, &run.ClassifierVersion, &run.InputHash, &timestamp, &run.UsedClassifier, &run.Reason, &candidates); err != nil {
			return err
		}
		if json.Unmarshal([]byte(candidates), &run.Candidates) != nil {
			return repository.ErrStorage
		}
		if len(run.Candidates) == 0 {
			run.Candidates = nil
		}
		run.Timestamp = instant(timestamp)
		email, categories, err := classificationInputs(ctx, tx, account, id)
		if err != nil {
			return err
		}
		if classification.InputHash(email, categories) != run.InputHash {
			return repository.ErrConflict
		}
		rows, err := tx.QueryContext(ctx, "SELECT category_id,confidence,classifier_name,classifier_version,input_hash,reasons_json,assessed_at,score_kind FROM classification_results WHERE account_id=? AND email_id=? ORDER BY confidence DESC,category_id", account, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			result := domain.ClassificationResult{AccountID: account, EmailID: id}
			var reasons string
			var assessed int64
			if err = rows.Scan(&result.CategoryID, &result.Confidence, &result.ClassifierName, &result.ClassifierVersion, &result.InputHash, &reasons, &assessed, &result.ScoreKind); err != nil {
				return err
			}
			if json.Unmarshal([]byte(reasons), &result.Reasons) != nil {
				return repository.ErrStorage
			}
			result.Timestamp = instant(assessed)
			run.Results = append(run.Results, result)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if !validClassificationRun(run) {
			return repository.ErrStorage
		}
		return nil
	})
	if err != nil {
		return domain.ClassificationRun{}, err
	}
	return run, nil
}
