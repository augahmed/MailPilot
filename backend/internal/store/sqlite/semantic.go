package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"sort"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

type semanticRepository struct{ store *Store }

func (s *Store) Semantic() repository.SemanticRepository { return &semanticRepository{s} }
func semanticSnapshot(ctx context.Context, tx *sql.Tx, a domain.AccountID, id domain.EmailID, p repository.IndexProfile) (repository.SemanticSnapshot, error) {
	s := repository.SemanticSnapshot{Profile: p, Availability: "ready", Chunks: []repository.VectorSource{}, Prototypes: []repository.SemanticPrototype{}}
	if !validProfile(p) || p.CategoryRecipe != repository.CategoryRecipe {
		return s, repository.ErrInvalid
	}
	email, categories, err := classificationInputs(ctx, tx, a, id)
	if err != nil {
		return s, err
	}
	s.Email.Email = email
	s.Categories = categories
	s.BaseHash = classification.InputHash(email, categories)
	if err = tx.QueryRowContext(ctx, `SELECT content_hash FROM emails WHERE account_id=? AND id=? AND remote_deleted=0`, a, id).Scan(&s.Email.ContentHash); err != nil {
		return s, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM classification_state WHERE account_id=?`, a).Scan(&s.Revision); err != nil {
		return s, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM classification_email_state WHERE account_id=? AND email_id=?`, a, id).Scan(&s.EmailRevision); err != nil {
		return s, err
	}
	err = tx.QueryRowContext(ctx, `SELECT mode,COALESCE(category_id,''),revision FROM email_category_preferences WHERE account_id=? AND email_id=?`, a, id).Scan(&s.Preference.Mode, &s.Preference.CategoryID, &s.Preference.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	state, err := currentIndex(ctx, tx, a, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, repository.ErrConflict) {
		return s, err
	}
	if err != nil || state.Status != repository.IndexReady || state.BodyState != "complete" || state.ContentStatus != "complete" || state.SourceRevision != s.Email.ContentHash || state.Model != p.Model || state.PreprocessingVersion != p.PreprocessingVersion || state.IndexVersion != p.IndexVersion {
		s.Availability = "email_vectors_unavailable"
	} else {
		s.Generation = state.Generation
		s.Chunks, err = semanticChunks(ctx, tx, a, id, p)
		if err != nil {
			return s, err
		}
		if len(s.Chunks) == 0 {
			s.Availability = "email_vectors_unavailable"
		}
	}
	for _, c := range categories {
		if !c.Enabled {
			continue
		}
		if len(s.Prototypes) >= repository.MaxSemanticCategories {
			return s, repository.ErrInvalid
		}
		proto := repository.SemanticPrototype{Category: c, Status: "pending"}
		var blob []byte
		err = tx.QueryRowContext(ctx, `SELECT v.source_hash,v.status,v.vector_data FROM category_prototypes v JOIN local_index_models m ON m.account_id=v.account_id AND m.model_id=v.model_id AND m.model_version=v.model_version WHERE v.account_id=? AND v.category_id=? AND v.profile_id=? AND m.fingerprint=? AND m.dimension=?`, a, c.ID, categoryProfile(p), p.Fingerprint, p.Model.Dimension).Scan(&proto.SourceHash, &proto.Status, &blob)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return s, err
		}
		if err == nil && (proto.Status == "ready" || proto.Status == "partial") {
			proto.Vector, err = decodeVector(blob, p.Model.Dimension)
			if err != nil {
				return s, err
			}
		} else {
			proto.Status = "pending"
			proto.SourceHash = ""
			s.Availability = "category_vectors_unavailable"
		}
		s.Prototypes = append(s.Prototypes, proto)
	}
	if len(s.Prototypes) == 0 {
		s.Availability = "no_enabled_categories"
	}
	s.Hash = categoryDigest(struct {
		Version, Revision, EmailRevision, BaseHash, Source, Generation string
		Profile                                                        repository.IndexProfile
	}{"semantic-snapshot-v1", s.Revision, s.EmailRevision, s.BaseHash, s.Email.ContentHash, s.Generation, p})
	return s, nil
}
func semanticChunks(ctx context.Context, tx *sql.Tx, a domain.AccountID, id domain.EmailID, p repository.IndexProfile) ([]repository.VectorSource, error) {
	q, args := vectorQuery(a, p, repository.SearchFilter{})
	args = append(args, id)
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.chunk_index,c.section_type,c.text,c.token_count,c.source_revision,c.start_offset,c.end_offset,c.content_hash,c.preprocessing_version,c.source_kind,c.section_index,c.section_start_offset,c.section_end_offset,c.context_text,v.vector_data,s.generation`+q+` AND e.id=? ORDER BY c.chunk_index,c.id LIMIT 4097`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []repository.VectorSource{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		v := repository.VectorSource{}
		c := &v.Chunk
		c.AccountID = a
		c.EmailID = id
		var blob []byte
		if err = rows.Scan(&c.ID, &c.Index, &c.SectionType, &c.Text, &c.TokenCount, &c.SourceRevision, &c.StartOffset, &c.EndOffset, &c.ContentHash, &c.PreprocessingVersion, &c.SourceKind, &c.SectionIndex, &c.SectionStartOffset, &c.SectionEndOffset, &c.ContextText, &blob, &v.Generation); err != nil {
			return nil, err
		}
		if !validChunk(c.ChunkInput) {
			return nil, repository.ErrStorage
		}
		v.Vector, err = decodeVector(blob, p.Model.Dimension)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	if len(result) > 4096 {
		return nil, repository.ErrInvalid
	}
	return result, rows.Err()
}
func (r *semanticRepository) Snapshot(ctx context.Context, a domain.AccountID, id domain.EmailID, p repository.IndexProfile) (repository.SemanticSnapshot, error) {
	var s repository.SemanticSnapshot
	err := r.store.transaction(ctx, func(tx *sql.Tx) error { var err error; s, err = semanticSnapshot(ctx, tx, a, id, p); return err })
	return s, err
}
func (r *semanticRepository) Next(ctx context.Context, run repository.IndexRun, version string) ([]domain.EmailID, error) {
	result := []domain.EmailID{}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, semanticCandidates+` ORDER BY e.id LIMIT 32`, run.AccountID, version)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id domain.EmailID
			if err = rows.Scan(&id); err != nil {
				return err
			}
			result = append(result, id)
		}
		return rows.Err()
	})
	return result, err
}
func (r *semanticRepository) SaveIndexed(ctx context.Context, run repository.IndexRun, result domain.ClassificationRun) error {
	if run.ClassifierVersion == "" || run.ClassifierVersion != result.ClassifierVersion || run.AccountID != result.AccountID || result.Semantic == nil || repository.IndexProfileFromSemantic(result.Semantic.Profile) != run.Profile {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		return replaceClassification(ctx, tx, result)
	})
}
func (r *semanticRepository) SetPreference(ctx context.Context, a domain.AccountID, e domain.EmailID, mode string, c domain.CategoryID, revision string) error {
	if a == "" || e == "" || !validHash(revision) || (mode != "assigned" && mode != "unclassified" && mode != "auto") || (mode == "assigned") != (c != "") {
		return repository.ErrInvalid
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, `SELECT 1 FROM emails WHERE account_id=? AND id=? AND content_hash=? AND remote_deleted=0`, a, e, revision); err != nil {
			return err
		}
		if mode == "auto" {
			_, err := tx.ExecContext(ctx, `DELETE FROM email_category_preferences WHERE account_id=? AND email_id=?`, a, e)
			return err
		}
		var category any
		if c != "" {
			if err := exists(ctx, tx, `SELECT 1 FROM categories WHERE account_id=? AND id=? AND enabled=1`, a, c); err != nil {
				return err
			}
			category = c
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO email_category_preferences(account_id,email_id,mode,category_id,revision) VALUES(?,?,?,?,?) ON CONFLICT(account_id,email_id) DO UPDATE SET mode=excluded.mode,category_id=excluded.category_id,revision=excluded.revision`, a, e, mode, category, newID())
		return err
	})
}

const semanticPending = `(r.email_id IS NULL OR r.classifier_version<>? OR json_extract(CASE WHEN json_valid(r.semantic_json) THEN r.semantic_json ELSE '{}' END,'$.Revision') IS NOT s.revision OR json_extract(CASE WHEN json_valid(r.semantic_json) THEN r.semantic_json ELSE '{}' END,'$.EmailRevision') IS NOT es.revision)`
const semanticSources = ` FROM emails e JOIN classification_state s ON s.account_id=e.account_id JOIN classification_email_state es ON es.account_id=e.account_id AND es.email_id=e.id LEFT JOIN classification_runs r ON r.account_id=e.account_id AND r.email_id=e.id WHERE e.account_id=? AND e.remote_deleted=0 AND length(e.content_hash)=64`
const semanticCandidates = `SELECT e.id` + semanticSources + ` AND ` + semanticPending

func semanticProgress(ctx context.Context, q indexQuery, a domain.AccountID, version string) (repository.SemanticProgress, error) {
	p := repository.SemanticProgress{}
	// Both counts describe the same SQLite snapshot during concurrent sync.
	if err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(SUM(`+semanticPending+`),0)`+semanticSources, version, a).Scan(&p.Eligible, &p.Pending); err != nil {
		return p, err
	}
	p.Assessed = p.Eligible - p.Pending
	return p, nil
}
func (r *semanticRepository) Progress(ctx context.Context, a domain.AccountID, version string) (repository.SemanticProgress, error) {
	p, e := semanticProgress(ctx, r.store.db, a, version)
	return p, storageError(e)
}
func (r *semanticRepository) Prepare(ctx context.Context, run repository.IndexRun, version string) (repository.IndexRun, error) {
	if !validName(version, 100) {
		return run, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkIndexRun(ctx, tx, run); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE local_index_jobs SET classifier_version=? WHERE account_id=? AND generation=?`, version, run.AccountID, run.Generation)
		return err
	})
	run.ClassifierVersion = version
	return run, err
}

func validSemanticEvidence(run domain.ClassificationRun) bool {
	e := run.Semantic
	if e == nil {
		return true
	}
	p := repository.IndexProfileFromSemantic(e.Profile)
	if e.Version != classification.SemanticVersion || !validProfile(p) || p.CategoryRecipe != repository.CategoryRecipe || !validHash(e.SourceRevision) || !validHash(e.BaseHash) || e.Revision == "" || e.EmailRevision == "" || len(e.Scores) > repository.MaxSemanticCategories || len(e.Assigned) > 1000 || e.MinimumScore <= 0 || e.MinimumScore > 1 || e.MinimumMargin < 0 || e.MinimumMargin > 1 {
		return false
	}
	switch e.State {
	case "suggested":
		if len(run.Results) == 0 || len(run.Results) > repository.MaxSemanticCategories {
			return false
		}
		if run.UsedClassifier == "semantic" {
			expected := classification.SuggestedScores(e.Scores, e.MinimumScore, e.MinimumMargin)
			if len(expected) != len(run.Results) {
				return false
			}
			for i, result := range run.Results {
				if result.CategoryID != expected[i].CategoryID || result.Confidence != expected[i].Score || result.ScoreKind != "cosine_similarity" {
					return false
				}
			}
		} else if run.UsedClassifier != "rules" || len(run.Results) != 1 || run.Results[0].ScoreKind != "rule_confidence" {
			return false
		}
	case "unclassified":
		if len(run.Results) != 0 {
			return false
		}
	case "assigned":
		if len(e.Assigned) == 0 || (len(e.Assigned) == 1) != (len(run.Results) == 1) {
			return false
		}
	default:
		return false
	}
	seen := map[domain.CategoryID]bool{}
	for i, s := range e.Scores {
		if s.CategoryID == "" || seen[s.CategoryID] || !finiteSemantic(s.Score, -1, 1) || !validHash(s.PrototypeHash) || (s.PrototypeStatus != "ready" && s.PrototypeStatus != "partial") || s.ChunkCount < 2 || s.ChunkCount > 4096 || len(s.Evidence) < 2 || len(s.Evidence) > 4 {
			return false
		}
		seen[s.CategoryID] = true
		if i > 0 {
			prev := e.Scores[i-1]
			if prev.Score < s.Score || prev.Score == s.Score && prev.CategoryID > s.CategoryID {
				return false
			}
		}
		chunks := map[string]bool{}
		for _, c := range s.Evidence {
			if c.ChunkID == "" || chunks[c.ChunkID] || !finiteSemantic(c.Similarity, -1, 1) || !finiteSemantic(c.Weight, 0, 1) || c.Weight <= 0 || len(c.Section) > 64 {
				return false
			}
			chunks[c.ChunkID] = true
		}
	}
	for _, r := range run.Results {
		if r.ScoreKind == "user_assignment" && (e.State != "assigned" || len(e.Assigned) != 1 || e.Assigned[0] != r.CategoryID || r.Confidence != 1) {
			return false
		}
	}
	return true
}
func finiteSemantic(v, lo, hi float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi
}
func semanticMatches(current repository.SemanticSnapshot, run domain.ClassificationRun) bool {
	e := run.Semantic
	if current.Hash != run.InputHash || current.Revision != e.Revision || current.EmailRevision != e.EmailRevision || current.BaseHash != e.BaseHash || current.Email.ContentHash != e.SourceRevision || current.Generation != e.ChunkGeneration {
		return false
	}
	assigned := []domain.CategoryID{}
	if current.Preference.Mode == "assigned" {
		assigned = append(assigned, current.Preference.CategoryID)
	} else if current.Preference.Mode != "unclassified" {
		for _, c := range current.Email.Email.Categories {
			assigned = append(assigned, c.ID)
		}
	}
	sort.Slice(assigned, func(i, j int) bool { return assigned[i] < assigned[j] })
	if !slices.Equal(assigned, e.Assigned) {
		return false
	}
	chunks := map[string]bool{}
	for _, c := range current.Chunks {
		chunks[c.Chunk.ID] = true
	}
	prototypes := map[domain.CategoryID]repository.SemanticPrototype{}
	for _, p := range current.Prototypes {
		prototypes[p.Category.ID] = p
	}
	for _, s := range e.Scores {
		p, ok := prototypes[s.CategoryID]
		if !ok || p.SourceHash != s.PrototypeHash || p.Status != s.PrototypeStatus {
			return false
		}
		for _, c := range s.Evidence {
			if !chunks[c.ChunkID] {
				return false
			}
		}
	}
	return true
}
