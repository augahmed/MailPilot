package sqlite

import (
	"context"
	"database/sql"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type indexRepository struct{ store *Store }

func (s *Store) Indexing() repository.IndexRepository { return &indexRepository{s} }
func validProfile(p repository.IndexProfile) bool {
	return (p.CategoryRecipe == "" || p.CategoryRecipe == repository.CategoryRecipe) && validModel(p.Model) && validName(p.PreprocessingVersion, 128) && validName(p.IndexVersion, 128) && validHash(p.Fingerprint)
}
func checkIndexRun(ctx context.Context, tx *sql.Tx, r repository.IndexRun) error {
	if r.AccountID == "" || !validName(r.Generation, 128) || !validProfile(r.Profile) {
		return repository.ErrInvalid
	}
	return checkIndexRunStatus(ctx, tx, r, "running")
}
func checkIndexRunStatus(ctx context.Context, tx *sql.Tx, r repository.IndexRun, status string) error {
	return exists(ctx, tx, `SELECT 1 FROM local_index_jobs WHERE account_id=? AND generation=? AND status=? AND model_id=? AND model_version=? AND dimension=? AND preprocessing_version=? AND index_version=? AND fingerprint=? AND category_recipe=? AND classifier_version=?`, r.AccountID, r.Generation, status, r.Profile.Model.ID, r.Profile.Model.Version, r.Profile.Model.Dimension, r.Profile.PreprocessingVersion, r.Profile.IndexVersion, r.Profile.Fingerprint, r.Profile.CategoryRecipe, r.ClassifierVersion)
}
func (r *indexRepository) Recover(ctx context.Context) error {
	_, err := r.store.db.ExecContext(ctx, `UPDATE local_index_jobs SET status='interrupted',code='interrupted',generation=lower(hex(randomblob(16))),updated_at=? WHERE status='running'`, nowMillis())
	return storageError(err)
}
func (r *indexRepository) Begin(ctx context.Context, a domain.AccountID, p repository.IndexProfile, rebuild bool) (repository.IndexRun, error) {
	run := repository.IndexRun{AccountID: a, Profile: p, Generation: newID(), Status: "running"}
	if a == "" || !validProfile(p) {
		return repository.IndexRun{}, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, `SELECT 1 FROM accounts WHERE id=? AND provider='gmail'`, a); err != nil {
			return err
		}
		var previousFingerprint string
		var previousDimension int
		err := tx.QueryRowContext(ctx, `SELECT fingerprint,dimension FROM local_index_models WHERE account_id=? AND model_id=? AND model_version=?`, a, p.Model.ID, p.Model.Version).Scan(&previousFingerprint, &previousDimension)
		if err == nil && (previousFingerprint != p.Fingerprint || previousDimension != p.Model.Dimension) {
			return repository.ErrConflict
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO local_index_models(account_id,model_id,model_version,dimension,fingerprint) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, a, p.Model.ID, p.Model.Version, p.Model.Dimension, p.Fingerprint); err != nil {
			return err
		}
		// Rebuild reprojects every message but preserves compatible cached vectors.
		if rebuild {
			if _, err := tx.ExecContext(ctx, `UPDATE index_state SET status='stale',generation=lower(hex(randomblob(16))),last_indexed_at=NULL,attempt_generation='',failure_code='' WHERE account_id=?`, a); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO local_index_jobs(account_id,generation,model_id,model_version,dimension,preprocessing_version,index_version,fingerprint,status,code,updated_at,category_recipe) VALUES(?,?,?,?,?,?,?,?,'running','',?,?) ON CONFLICT(account_id) DO UPDATE SET generation=excluded.generation,model_id=excluded.model_id,model_version=excluded.model_version,dimension=excluded.dimension,preprocessing_version=excluded.preprocessing_version,index_version=excluded.index_version,fingerprint=excluded.fingerprint,category_recipe=excluded.category_recipe,classifier_version='',status='running',code='',updated_at=excluded.updated_at`, a, run.Generation, p.Model.ID, p.Model.Version, p.Model.Dimension, p.PreprocessingVersion, p.IndexVersion, p.Fingerprint, nowMillis(), p.CategoryRecipe)
		return err
	})
	if err != nil {
		return repository.IndexRun{}, err
	}
	return run, nil
}
func (r *indexRepository) Run(ctx context.Context, a domain.AccountID) (repository.IndexRun, error) {
	run := repository.IndexRun{AccountID: a}
	if a == "" {
		return run, repository.ErrInvalid
	}
	err := r.store.db.QueryRowContext(ctx, `SELECT generation,model_id,model_version,dimension,preprocessing_version,index_version,fingerprint,status,code,category_recipe,classifier_version FROM local_index_jobs WHERE account_id=?`, a).Scan(&run.Generation, &run.Profile.Model.ID, &run.Profile.Model.Version, &run.Profile.Model.Dimension, &run.Profile.PreprocessingVersion, &run.Profile.IndexVersion, &run.Profile.Fingerprint, &run.Status, &run.Code, &run.Profile.CategoryRecipe, &run.ClassifierVersion)
	return run, storageError(err)
}

// Enumerate the entire live cache, independent of mailbox paging/Inbox labels.
// A failed immutable input is attempted once per run; Resume retries failures.
const indexCandidates = `SELECT e.id,e.content_hash FROM emails e LEFT JOIN index_state s ON s.account_id=e.account_id AND s.email_id=e.id
WHERE e.account_id=? AND e.remote_deleted=0 AND length(e.content_hash)=64 AND e.content_hash NOT GLOB '*[^0-9a-f]*'
AND NOT (COALESCE(s.source_revision=e.content_hash AND s.body_state=e.body_state AND s.model_id=? AND s.model_version=? AND s.dimension=? AND s.preprocessing_version=? AND s.index_version=? AND s.attempt_generation=?,0))
AND (s.email_id IS NULL OR s.source_revision<>e.content_hash OR s.body_state<>e.body_state OR s.model_id<>? OR s.model_version<>? OR s.dimension<>? OR s.preprocessing_version<>? OR s.index_version<>? OR s.status='stale' OR s.failure_code<>''
 OR EXISTS(SELECT 1 FROM email_chunks c LEFT JOIN chunk_embeddings v ON v.account_id=c.account_id AND v.chunk_id=c.id AND v.model_id=? AND v.model_version=? AND v.dimension=? WHERE c.account_id=e.account_id AND c.email_id=e.id AND (v.status IS NULL OR v.status<>'ready')))
ORDER BY e.id LIMIT 32`

func nextIndex(ctx context.Context, tx *sql.Tx, run repository.IndexRun) ([]repository.IndexCandidate, error) {
	p := run.Profile
	rows, err := tx.QueryContext(ctx, indexCandidates, run.AccountID, p.Model.ID, p.Model.Version, p.Model.Dimension, p.PreprocessingVersion, p.IndexVersion, run.Generation, p.Model.ID, p.Model.Version, p.Model.Dimension, p.PreprocessingVersion, p.IndexVersion, p.Model.ID, p.Model.Version, p.Model.Dimension)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []repository.IndexCandidate{}
	for rows.Next() {
		var c repository.IndexCandidate
		if err = rows.Scan(&c.EmailID, &c.SourceRevision); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (r *indexRepository) Next(ctx context.Context, run repository.IndexRun) ([]repository.IndexCandidate, error) {
	var result []repository.IndexCandidate
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		var err error
		result, err = nextIndex(ctx, tx, run)
		return err
	})
	return result, err
}
func (r *indexRepository) Transition(ctx context.Context, run repository.IndexRun, status, code string) error {
	switch status {
	case "paused", "cancelled", "interrupted", "complete", "partial", "failed":
	default:
		return repository.ErrInvalid
	}
	switch code {
	case "", "interrupted", "cache_changed", "model_unavailable", "inference_failed", "storage_error":
	default:
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRunStatus(ctx, tx, run, run.Status); err != nil {
			return err
		}
		if status == "complete" || status == "partial" {
			rows, err := nextIndex(ctx, tx, run)
			if err != nil {
				return err
			}
			if len(rows) > 0 {
				return repository.ErrConflict
			}
			counts, err := indexProgress(ctx, tx, run.AccountID, run.Profile)
			if err != nil {
				return err
			}
			categoryCounts := repository.CategoryVectorProgress{}
			if run.Profile.CategoryRecipe != "" {
				categoryCounts, err = categoryVectorProgress(ctx, tx, run.AccountID, run.Profile)
				if err != nil {
					return err
				}
				if categoryCounts.Pending > 0 {
					return repository.ErrConflict
				}
			}
			if run.ClassifierVersion != "" {
				semantic, err := semanticProgress(ctx, tx, run.AccountID, run.ClassifierVersion)
				if err != nil {
					return err
				}
				if semantic.Pending > 0 {
					return repository.ErrConflict
				}
			}
			status = "complete"
			if counts.Partial > 0 || counts.Failed > 0 || counts.Unavailable > 0 || categoryCounts.Partial > 0 {
				status = "partial"
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE local_index_jobs SET status=?,code=?,updated_at=? WHERE account_id=? AND generation=?`, status, code, nowMillis(), run.AccountID, run.Generation)
		return err
	})
}
func (r *indexRepository) ReplaceIndexed(ctx context.Context, run repository.IndexRun, s repository.ChunkSnapshot) (repository.IndexState, error) {
	return (&chunkRepository{r.store}).replace(ctx, s, &run)
}
func (r *indexRepository) SaveIndexed(ctx context.Context, run repository.IndexRun, b repository.EmbeddingBatch) (repository.IndexState, error) {
	return (&chunkRepository{r.store}).saveEmbeddings(ctx, b, &run)
}
func (r *indexRepository) Fail(ctx context.Context, run repository.IndexRun, c repository.IndexCandidate, code string) error {
	if code != "invalid_content" && code != "inference_failed" && code != "invalid_vector" {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		revision, body, err := chunkSource(ctx, tx, run.AccountID, c.EmailID)
		if err != nil {
			return err
		}
		if revision != c.SourceRevision {
			return repository.ErrConflict
		}
		p := run.Profile
		// A failed source projection never makes old chunks readable as current.
		_, err = tx.ExecContext(ctx, `INSERT INTO index_state(account_id,email_id,source_revision,preprocessing_version,index_version,model_id,model_version,dimension,generation,body_state,status,content_status,attempt_generation,failure_code) VALUES(?,?,?,?,?,?,?,?,?,?,'stale','partial',?,?) ON CONFLICT(account_id,email_id) DO UPDATE SET source_revision=excluded.source_revision,preprocessing_version=excluded.preprocessing_version,index_version=excluded.index_version,model_id=excluded.model_id,model_version=excluded.model_version,dimension=excluded.dimension,generation=excluded.generation,body_state=excluded.body_state,status='stale',last_indexed_at=NULL,attempt_generation=excluded.attempt_generation,failure_code=excluded.failure_code`, run.AccountID, c.EmailID, revision, p.PreprocessingVersion, p.IndexVersion, p.Model.ID, p.Model.Version, p.Model.Dimension, newID(), body, run.Generation, code)
		return err
	})
}

// Current counts only: no historical, stale, unrelated-account or incompatible vectors.
func (r *indexRepository) Progress(ctx context.Context, a domain.AccountID, p repository.IndexProfile) (repository.IndexProgress, error) {
	return indexProgress(ctx, r.store.db, a, p)
}

type indexQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func indexProgress(ctx context.Context, q indexQuery, a domain.AccountID, p repository.IndexProfile) (repository.IndexProgress, error) {
	var result repository.IndexProgress
	if a == "" || !validProfile(p) {
		return result, repository.ErrInvalid
	}
	err := q.QueryRowContext(ctx, `WITH identity AS (
 SELECT NOT EXISTS(SELECT 1 FROM local_index_models WHERE account_id=? AND model_id=? AND model_version=? AND (fingerprint<>? OR dimension<>?)) AS compatible
), counts AS (
 SELECT c.email_id,COUNT(*) AS chunks,SUM(CASE WHEN v.status='ready' THEN 1 ELSE 0 END) AS embeddings
 FROM email_chunks c LEFT JOIN chunk_embeddings v ON v.account_id=c.account_id AND v.chunk_id=c.id AND v.model_id=? AND v.model_version=? AND v.dimension=?
 WHERE c.account_id=? GROUP BY c.email_id
), coverage AS (
 SELECT length(e.content_hash)=64 AND e.content_hash NOT GLOB '*[^0-9a-f]*' AS source_valid,s.status,s.failure_code,s.content_status,COALESCE(c.chunks,0) AS chunks,COALESCE(c.embeddings,0) AS embeddings,
 COALESCE(s.source_revision=e.content_hash AND s.body_state=e.body_state AND s.model_id=? AND s.model_version=? AND s.dimension=? AND s.preprocessing_version=? AND s.index_version=? AND (SELECT compatible FROM identity),0) AS compatible
 FROM emails e LEFT JOIN index_state s ON s.account_id=e.account_id AND s.email_id=e.id LEFT JOIN counts c ON c.email_id=e.id WHERE e.account_id=? AND e.remote_deleted=0
), classified AS (
 SELECT *,CASE WHEN source_valid=0 THEN 'unavailable' WHEN compatible=0 THEN 'pending' WHEN failure_code<>'' THEN 'failed' WHEN status='stale' THEN 'pending' WHEN chunks=0 THEN 'unavailable' WHEN chunks=embeddings AND content_status='complete' THEN 'complete' WHEN embeddings>0 THEN 'partial' ELSE 'pending' END AS state FROM coverage
)
SELECT COUNT(*),COALESCE(SUM(state IN ('complete','partial') AND chunks=embeddings),0),COALESCE(SUM(state='complete'),0),COALESCE(SUM(state='partial'),0),COALESCE(SUM(state='failed'),0),COALESCE(SUM(state='pending'),0),COALESCE(SUM(state='unavailable'),0),COALESCE(SUM(CASE WHEN compatible=1 AND status<>'stale' THEN chunks ELSE 0 END),0),COALESCE(SUM(CASE WHEN compatible=1 AND status<>'stale' THEN embeddings ELSE 0 END),0) FROM classified`, a, p.Model.ID, p.Model.Version, p.Fingerprint, p.Model.Dimension, p.Model.ID, p.Model.Version, p.Model.Dimension, a, p.Model.ID, p.Model.Version, p.Model.Dimension, p.PreprocessingVersion, p.IndexVersion, a).Scan(&result.Eligible, &result.Indexed, &result.Complete, &result.Partial, &result.Failed, &result.Pending, &result.Unavailable, &result.Chunks, &result.Embeddings)
	return result, storageError(err)
}
