package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"strings"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/search"
)

func retrievalTable(m search.Model) string {
	if m.Version == embedding.RetrievalVersion {
		return "email_retrieval_indexes"
	}
	return "email_embeddings"
}

type searchRepository struct{ store *Store }

func (s *Store) SearchIndex() search.Repository { return &searchRepository{s} }
func (r *searchRepository) Counts(ctx context.Context, a domain.AccountID, m search.Model) (search.Counts, error) {
	var c search.Counts
	if a == "" || m.Validate() != nil {
		return c, repository.ErrInvalid
	}
	err := r.store.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(e.body_state='complete'),0),coalesce(sum(e.body_state='complete' AND v.email_id IS NOT NULL),0) FROM emails e LEFT JOIN `+retrievalTable(m)+` v ON v.account_id=e.account_id AND v.email_id=e.id AND v.provider=? AND v.dimension=? AND v.preprocessing_version=? WHERE e.remote_deleted=0 AND e.account_id=?`, m.Provider, m.Dimension, m.Version, a).Scan(&c.Cached, &c.Eligible, &c.Indexed)
	return c, storageError(err)
}
func (r *searchRepository) Missing(ctx context.Context, a domain.AccountID, m search.Model, after domain.EmailID, limit int) ([]domain.EmailID, error) {
	if a == "" || m.Validate() != nil || limit < 1 || limit > embedding.MaxBatchSize {
		return nil, repository.ErrInvalid
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT e.id FROM emails e LEFT JOIN `+retrievalTable(m)+` v ON v.account_id=e.account_id AND v.email_id=e.id AND v.provider=? AND v.dimension=? AND v.preprocessing_version=? WHERE e.remote_deleted=0 AND e.account_id=? AND e.body_state='complete' AND e.id>? AND v.email_id IS NULL ORDER BY e.id LIMIT ?`, m.Provider, m.Dimension, m.Version, a, after, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []domain.EmailID{}
	for rows.Next() {
		var id domain.EmailID
		if err := rows.Scan(&id); err != nil {
			return nil, storageError(err)
		}
		result = append(result, id)
	}
	return result, storageError(rows.Err())
}
func loadSearchCategories(ctx context.Context, tx *sql.Tx, a domain.AccountID) ([]domain.EmailCategory, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,description,enabled,created_at,updated_at FROM categories WHERE account_id=? ORDER BY id", a)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.EmailCategory
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func categoryMatches(ctx context.Context, tx *sql.Tx, e domain.Email, m search.Model, f search.Filters, categories []domain.EmailCategory) (bool, error) {
	if f.CategoryID == "" {
		return true, nil
	}
	for _, assigned := range e.Categories {
		if assigned.ID == f.CategoryID {
			return true, nil
		}
	}
	var hash, name, version string
	err := tx.QueryRowContext(ctx, `SELECT r.input_hash,r.classifier_name,r.classifier_version FROM classification_runs r JOIN classification_results p ON p.account_id=r.account_id AND p.email_id=r.email_id WHERE r.account_id=? AND r.email_id=? AND p.category_id=?`, e.AccountID, e.ID, f.CategoryID).Scan(&hash, &name, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == m.ClassifierName && version == m.ClassifierVersion && hash == classification.InputHash(e, categories), nil
}
func (r *searchRepository) Candidates(ctx context.Context, a domain.AccountID, m search.Model, f search.Filters, after domain.EmailID, limit int) (search.Page, error) {
	page := search.Page{Candidates: []search.Candidate{}}
	if a == "" || m.Validate() != nil || f.Validate() != nil || limit < 1 || limit > search.PageSize {
		return page, repository.ErrInvalid
	}
	// Parameters only: sender is a literal substring, never Gmail query syntax or SQL.
	vectorColumn := "v.vector"
	if m.Version == embedding.RetrievalVersion {
		vectorColumn = "NULL"
	}
	query := `SELECT e.id,v.input_hash,` + vectorColumn + ` FROM ` + retrievalTable(m) + ` v JOIN emails e ON e.account_id=v.account_id AND e.id=v.email_id JOIN email_addresses sender ON sender.email_id=e.id AND sender.role='sender' WHERE e.remote_deleted=0 AND v.account_id=? AND v.provider=? AND v.dimension=? AND v.preprocessing_version=? AND e.body_state='complete' AND e.id>?`
	args := []any{a, m.Provider, m.Dimension, m.Version, after}
	if f.From != nil {
		query += " AND e.received_at>=?"
		args = append(args, *f.From)
	}
	if f.Until != nil {
		query += " AND e.received_at<?"
		args = append(args, *f.Until)
	}
	if strings.TrimSpace(f.Sender) != "" {
		query += " AND instr(lower(sender.name||' '||sender.address),lower(?))>0"
		args = append(args, strings.TrimSpace(f.Sender))
	}
	if f.CategoryID != "" {
		query += ` AND (EXISTS(SELECT 1 FROM email_categories ec WHERE ec.account_id=e.account_id AND ec.email_id=e.id AND ec.category_id=?) OR EXISTS(SELECT 1 FROM classification_results cr WHERE cr.account_id=e.account_id AND cr.email_id=e.id AND cr.category_id=?))`
		args = append(args, f.CategoryID, f.CategoryID)
	}
	query += " ORDER BY e.id LIMIT ?"
	args = append(args, limit)
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		type row struct {
			id   domain.EmailID
			hash string
			data []byte
		}
		var records []row
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.hash, &item.data); err != nil {
				_ = rows.Close()
				return err
			}
			records = append(records, item)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		page.Scanned = len(records)
		page.Done = len(records) < limit
		page.After = after
		var categories []domain.EmailCategory
		if f.CategoryID != "" {
			categories, err = loadSearchCategories(ctx, tx, a)
			if err != nil {
				return err
			}
		}
		for _, record := range records {
			page.After = record.id
			source, err := scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE remote_deleted=0 AND account_id=? AND id=?", a, record.id))
			if err != nil {
				return err
			}
			if err = loadEmailRelations(ctx, tx, &source.Email); err != nil {
				return err
			}
			match, err := categoryMatches(ctx, tx, source.Email, m, f, categories)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			candidate := search.Candidate{Stored: source, InputHash: record.hash}
			if m.Version == embedding.RetrievalVersion {
				prepared, prepareErr := embedding.PrepareRetrieval(source.Email)
				values, cacheErr := passageCache(ctx, tx, a, record.id, m)
				if cacheErr != nil {
					return cacheErr
				}
				candidate.Valid = prepareErr == nil && prepared.Hash == record.hash
				for _, p := range prepared.Passages {
					v := values[p.Hash]
					if v == nil {
						candidate.Valid = false
					}
					candidate.Chunks = append(candidate.Chunks, search.ChunkVector{Passage: p, Vector: v})
				}
				if !candidate.Valid {
					if _, err := tx.ExecContext(ctx, `DELETE FROM email_retrieval_indexes WHERE account_id=? AND email_id=? AND provider=? AND dimension=? AND preprocessing_version=?`, a, record.id, m.Provider, m.Dimension, m.Version); err != nil {
						return err
					}
				}
				page.Candidates = append(page.Candidates, candidate)
				continue
			}

			if len(record.data) == m.Dimension*4 {
				candidate.Vector = make([]float32, m.Dimension)
				for i := range candidate.Vector {
					candidate.Vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(record.data[i*4:]))
				}
				candidate.Valid = embedding.ValidateVector(candidate.Vector, m.Dimension) == nil
			}
			if !candidate.Valid {
				// Discard corrupt derived data; a later explicit index run can rebuild it.
				if _, err := tx.ExecContext(ctx, `DELETE FROM email_embeddings WHERE account_id=? AND email_id=? AND provider=? AND dimension=? AND preprocessing_version=?`, a, record.id, m.Provider, m.Dimension, m.Version); err != nil {
					return err
				}
			}
			page.Candidates = append(page.Candidates, candidate)
		}
		return nil
	})
	return page, err
}
func (r *searchRepository) Current(ctx context.Context, a domain.AccountID, m search.Model, f search.Filters, id domain.EmailID) (repository.StoredEmail, error) {
	var source repository.StoredEmail
	if a == "" || id == "" || m.Validate() != nil || f.Validate() != nil {
		return source, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		source, err = scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE remote_deleted=0 AND account_id=? AND id=?", a, id))
		if err != nil {
			return err
		}
		if err = loadEmailRelations(ctx, tx, &source.Email); err != nil {
			return err
		}
		e := source.Email
		if e.BodyState != "complete" || f.From != nil && e.ReceivedAt.UnixMilli() < *f.From || f.Until != nil && e.ReceivedAt.UnixMilli() >= *f.Until || strings.TrimSpace(f.Sender) != "" && !strings.Contains(strings.ToLower(e.Sender.Name+" "+e.Sender.Address), strings.ToLower(strings.TrimSpace(f.Sender))) {
			return repository.ErrConflict
		}
		prepared, err := embedding.Prepare(e)
		if m.Version == embedding.RetrievalVersion {
			var doc embedding.RetrievalText
			doc, err = embedding.PrepareRetrieval(e)
			prepared.Hash = doc.Hash
		}
		if err != nil {
			return repository.ErrConflict
		}
		var hash string
		if err = tx.QueryRowContext(ctx, `SELECT input_hash FROM `+retrievalTable(m)+` WHERE account_id=? AND email_id=? AND provider=? AND dimension=? AND preprocessing_version=?`, a, id, m.Provider, m.Dimension, m.Version).Scan(&hash); err != nil {
			return err
		}
		if hash != prepared.Hash {
			return repository.ErrConflict
		}
		var categories []domain.EmailCategory
		if f.CategoryID != "" {
			categories, err = loadSearchCategories(ctx, tx, a)
			if err != nil {
				return err
			}
		}
		match, err := categoryMatches(ctx, tx, e, m, f, categories)
		if err != nil {
			return err
		}
		if !match {
			return repository.ErrConflict
		}
		return nil
	})
	return source, err
}
