package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type categoryVectorRepository struct{ store *Store }

func (s *Store) CategoryVectors() repository.CategoryVectorRepository {
	return &categoryVectorRepository{s}
}
func categoryDigest(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func categoryProfile(p repository.IndexProfile) string { return categoryDigest(p) }
func (r *categoryVectorRepository) Confirm(ctx context.Context, a domain.AccountID, c domain.CategoryID, e domain.EmailID, revision string) error {
	if a == "" || c == "" || e == "" || !validHash(revision) {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, `SELECT 1 FROM categories WHERE account_id=? AND id=? AND enabled=1`, a, c); err != nil {
			return err
		}
		if err := exists(ctx, tx, `SELECT 1 FROM emails WHERE account_id=? AND id=? AND remote_deleted=0 AND content_hash=? AND body_state='complete'`, a, e, revision); err != nil {
			return err
		}
		var count, already int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(email_id=?),0) FROM category_examples WHERE account_id=? AND category_id=?`, e, a, c).Scan(&count, &already); err != nil {
			return err
		}
		if count >= repository.MaxCategoryExamples && already == 0 {
			return repository.ErrInvalid
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO category_examples(account_id,category_id,email_id,source_revision,confirmation_id,confirmed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(account_id,category_id,email_id) DO UPDATE SET source_revision=excluded.source_revision,confirmation_id=excluded.confirmation_id,confirmed_at=excluded.confirmed_at`, a, c, e, revision, newID(), nowMillis())
		return err
	})
}
func (r *categoryVectorRepository) Remove(ctx context.Context, a domain.AccountID, c domain.CategoryID, e domain.EmailID) error {
	if a == "" || c == "" || e == "" {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, `SELECT 1 FROM categories WHERE account_id=? AND id=?`, a, c); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM category_examples WHERE account_id=? AND category_id=? AND email_id=?`, a, c, e)
		return err
	})
}
func (r *categoryVectorRepository) Source(ctx context.Context, a domain.AccountID, c domain.CategoryID, p repository.IndexProfile) (repository.CategoryVectorSource, error) {
	var source repository.CategoryVectorSource
	if !validSearchProfile(a, p) || p.CategoryRecipe != repository.CategoryRecipe || c == "" {
		return source, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error { var err error; source, err = categorySource(ctx, tx, a, c, p); return err })
	return source, err
}
func categorySource(ctx context.Context, tx *sql.Tx, a domain.AccountID, id domain.CategoryID, p repository.IndexProfile) (repository.CategoryVectorSource, error) {
	s := repository.CategoryVectorSource{AccountID: a, Profile: p, Examples: []repository.CategoryExample{}}
	c := &s.Category
	var created, updated int64
	err := tx.QueryRowContext(ctx, `SELECT id,name,description,enabled,created_at,updated_at,vector_revision FROM categories WHERE account_id=? AND id=?`, a, id).Scan(&c.ID, &c.Name, &c.Description, &c.Enabled, &created, &updated, &s.Revision)
	if err != nil {
		return s, storageError(err)
	}
	c.CreatedAt = instant(created)
	c.UpdatedAt = instant(updated)
	s.DefinitionHash = categoryDigest(struct {
		Name, Description string
		Profile           repository.IndexProfile
	}{c.Name, c.Description, p})
	rows, err := tx.QueryContext(ctx, `SELECT x.email_id,x.confirmation_id,x.source_revision,e.content_hash,e.subject FROM category_examples x JOIN emails e ON e.account_id=x.account_id AND e.id=x.email_id WHERE x.account_id=? AND x.category_id=? AND e.remote_deleted=0 ORDER BY x.email_id LIMIT 11`, a, id)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var e repository.CategoryExample
		if err = rows.Scan(&e.EmailID, &e.ConfirmationID, &e.ConfirmedRevision, &e.CurrentRevision, &e.Subject); err != nil {
			_ = rows.Close()
			return s, err
		}
		s.Examples = append(s.Examples, e)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return s, err
	}
	if len(s.Examples) > repository.MaxCategoryExamples {
		return s, repository.ErrStorage
	}
	for i := range s.Examples {
		e := &s.Examples[i]
		e.Status = "source_changed"
		if e.ConfirmedRevision != e.CurrentRevision {
			continue
		}
		state, err := currentIndex(ctx, tx, a, e.EmailID)
		e.Status = "index_pending"
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrConflict) {
			continue
		}
		if err != nil {
			return s, err
		}
		e.Generation = state.Generation
		if state.Status != repository.IndexReady || state.Model != p.Model || state.PreprocessingVersion != p.PreprocessingVersion || state.IndexVersion != p.IndexVersion {
			continue
		}
		e.Chunks, err = categoryExampleChunks(ctx, tx, a, e.EmailID, p)
		if err != nil {
			return s, err
		}
		if len(e.Chunks) > 0 {
			e.Status = "included"
		}
	}
	// Cache identity includes explicit confirmation, original revision, selected
	// current chunk generations/vectors, recipe and category's nonreused edit fence.
	s.SourceHash = categoryDigest(struct {
		AccountID            domain.AccountID
		CategoryID           domain.CategoryID
		Revision, Definition string
		Enabled              bool
		Examples             []repository.CategoryExample
	}{a, id, s.Revision, s.DefinitionHash, c.Enabled, s.Examples})
	var cached repository.CategoryPrototype
	var definition, vector []byte
	var inputs string
	var dimension int
	err = tx.QueryRowContext(ctx, `SELECT source_hash,definition_hash,status,definition_inputs_json,definition_vector,vector_data,dimension FROM category_prototypes WHERE account_id=? AND category_id=? AND profile_id=? AND model_id=? AND model_version=? AND fingerprint=?`, a, id, categoryProfile(p), p.Model.ID, p.Model.Version, p.Fingerprint).Scan(&cached.SourceHash, &cached.DefinitionHash, &cached.Status, &inputs, &definition, &vector, &dimension)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if dimension != p.Model.Dimension {
		return s, repository.ErrStorage
	}
	if cached.DefinitionHash == s.DefinitionHash {
		cached.DefinitionVector, err = decodeVector(definition, dimension)
		if err != nil {
			return s, err
		}
		if len(inputs) > 16384 || json.Unmarshal([]byte(inputs), &cached.DefinitionInputs) != nil {
			return s, repository.ErrStorage
		}
	}
	if cached.SourceHash == s.SourceHash && c.Enabled && (cached.Status == "ready" || cached.Status == "partial") {
		cached.Vector, err = decodeVector(vector, dimension)
		if err != nil {
			return s, err
		}
	} else {
		cached.Status = "stale"
	}
	s.Cached = &cached
	return s, nil
}
func categoryExampleChunks(ctx context.Context, tx *sql.Tx, a domain.AccountID, e domain.EmailID, p repository.IndexProfile) ([]repository.VectorSource, error) {
	q, args := vectorQuery(a, p, repository.SearchFilter{})
	args = append(args, e)
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.chunk_index,c.section_type,c.text,c.token_count,c.source_revision,c.start_offset,c.end_offset,c.content_hash,c.preprocessing_version,c.source_kind,c.section_index,c.section_start_offset,c.section_end_offset,c.context_text,v.vector_data,s.generation`+q+` AND e.id=? ORDER BY CASE c.section_type WHEN 'authored' THEN 0 WHEN 'forwarded' THEN 1 WHEN 'quoted' THEN 2 WHEN 'subject' THEN 3 ELSE 4 END,c.chunk_index,c.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selected := []repository.VectorSource{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var v repository.VectorSource
		c := &v.Chunk
		c.AccountID = a
		c.EmailID = e
		var blob []byte
		if err = rows.Scan(&c.ID, &c.Index, &c.SectionType, &c.Text, &c.TokenCount, &c.SourceRevision, &c.StartOffset, &c.EndOffset, &c.ContentHash, &c.PreprocessingVersion, &c.SourceKind, &c.SectionIndex, &c.SectionStartOffset, &c.SectionEndOffset, &c.ContextText, &blob, &v.Generation); err != nil {
			return nil, err
		}
		if !validChunk(c.ChunkInput) {
			return nil, repository.ErrStorage
		}
		duplicate := false
		for _, old := range selected {
			d := old.Chunk
			if d.ContentHash == c.ContentHash || c.SourceKind != "" && c.SourceKind == d.SourceKind && c.SectionIndex == d.SectionIndex && c.StartOffset < d.EndOffset && d.StartOffset < c.EndOffset {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		v.Vector, err = decodeVector(blob, p.Model.Dimension)
		if err != nil {
			return nil, err
		}
		selected = append(selected, v)
		if len(selected) == repository.MaxExampleChunks {
			break
		}
	}
	return selected, rows.Err()
}
func (r *categoryVectorRepository) Save(ctx context.Context, run repository.IndexRun, source repository.CategoryVectorSource, prototype repository.CategoryPrototype) error {
	if source.AccountID != run.AccountID || source.Profile != run.Profile || run.Profile.CategoryRecipe != repository.CategoryRecipe || !validHash(source.SourceHash) || prototype.SourceHash != source.SourceHash || prototype.DefinitionHash != source.DefinitionHash || len(prototype.DefinitionInputs) == 0 || len(prototype.DefinitionInputs) > 20 {
		return repository.ErrInvalid
	}
	vector, err := encodeVector(prototype.Vector, run.Profile.Model.Dimension)
	if err != nil {
		return err
	}
	definition, err := encodeVector(prototype.DefinitionVector, run.Profile.Model.Dimension)
	if err != nil {
		return err
	}
	data, err := json.Marshal(prototype.DefinitionInputs)
	if err != nil || len(data) > 16384 {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		current, err := categorySource(ctx, tx, run.AccountID, source.Category.ID, run.Profile)
		if err != nil {
			return err
		}
		if !current.Category.Enabled || current.SourceHash != source.SourceHash {
			return repository.ErrConflict
		}
		status := "ready"
		for _, e := range current.Examples {
			if e.Status != "included" {
				status = "partial"
			}
		}
		if prototype.Status != status {
			return repository.ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO category_prototypes(account_id,category_id,profile_id,model_id,model_version,dimension,fingerprint,preprocessing_version,index_version,recipe,definition_hash,source_hash,definition_inputs_json,definition_vector,vector_data,status,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,category_id,profile_id) DO UPDATE SET definition_hash=excluded.definition_hash,source_hash=excluded.source_hash,definition_inputs_json=excluded.definition_inputs_json,definition_vector=excluded.definition_vector,vector_data=excluded.vector_data,status=excluded.status,updated_at=excluded.updated_at`, run.AccountID, source.Category.ID, categoryProfile(run.Profile), run.Profile.Model.ID, run.Profile.Model.Version, run.Profile.Model.Dimension, run.Profile.Fingerprint, run.Profile.PreprocessingVersion, run.Profile.IndexVersion, run.Profile.CategoryRecipe, source.DefinitionHash, source.SourceHash, string(data), definition, vector, status, nowMillis())
		return err
	})
}
func categoryVectorProgress(ctx context.Context, q indexQuery, a domain.AccountID, p repository.IndexProfile) (repository.CategoryVectorProgress, error) {
	var c repository.CategoryVectorProgress
	err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(v.status='ready'),0),COALESCE(sum(v.status='partial'),0),COALESCE(sum(v.status IS NULL OR v.status='stale'),0) FROM categories c LEFT JOIN category_prototypes v ON v.account_id=c.account_id AND v.category_id=c.id AND v.profile_id=? WHERE c.account_id=? AND c.enabled=1`, categoryProfile(p), a).Scan(&c.Eligible, &c.Ready, &c.Partial, &c.Pending)
	return c, err
}
func (r *categoryVectorRepository) Progress(ctx context.Context, a domain.AccountID, p repository.IndexProfile) (repository.CategoryVectorProgress, error) {
	if !validSearchProfile(a, p) || p.CategoryRecipe != repository.CategoryRecipe {
		return repository.CategoryVectorProgress{}, repository.ErrInvalid
	}
	c, e := categoryVectorProgress(ctx, r.store.db, a, p)
	return c, storageError(e)
}
func (r *categoryVectorRepository) Next(ctx context.Context, run repository.IndexRun) ([]domain.CategoryID, error) {
	result := []domain.CategoryID{}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id FROM categories c LEFT JOIN category_prototypes v ON v.account_id=c.account_id AND v.category_id=c.id AND v.profile_id=? WHERE c.account_id=? AND c.enabled=1 AND (v.status IS NULL OR v.status='stale') ORDER BY c.id LIMIT 32`, categoryProfile(run.Profile), run.AccountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id domain.CategoryID
			if err = rows.Scan(&id); err != nil {
				return err
			}
			result = append(result, id)
		}
		return rows.Err()
	})
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, err
}
