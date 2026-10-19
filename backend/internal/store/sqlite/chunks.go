package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

const (
	maxChunks         = 4096
	maxChunkBytes     = 64 * 1024
	maxSnapshotBytes  = 4 * 1024 * 1024
	maxEmbeddingBatch = 100
)

type chunkRepository struct{ store *Store }

func (s *Store) Chunks() repository.ChunkRepository { return &chunkRepository{s} }

func validName(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validModel(m repository.EmbeddingModel) bool {
	return validName(m.ID, 128) && validName(m.Version, 128) && m.Dimension > 0 && m.Dimension <= 8192
}
func hashText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func validChunk(c repository.ChunkInput) bool {
	return validMapping(c) && c.Index >= 0 && c.Index < maxChunks && validName(c.SectionType, 64) && len(c.Text) > 0 && len(c.Text) <= maxChunkBytes &&
		utf8.ValidString(c.Text) && !strings.ContainsRune(c.Text, 0) && strings.TrimSpace(c.Text) != "" && c.TokenCount > 0 && c.TokenCount <= maxChunkBytes &&
		c.StartOffset >= 0 && c.EndOffset > c.StartOffset && c.EndOffset <= maxSnapshotBytes && c.EndOffset-c.StartOffset == len(c.Text) && c.ContentHash == hashText(c.Text)
}

func validMapping(c repository.ChunkInput) bool {
	if len(c.ContextText) > 1024 || !utf8.ValidString(c.ContextText) || strings.ContainsRune(c.ContextText, 0) {
		return false
	}
	if c.SourceKind == "" {
		return c.SectionIndex == 0 && c.SectionStartOffset == 0 && c.SectionEndOffset == 0
	}
	return (c.SourceKind == "subject" || c.SourceKind == "body_text" || c.SourceKind == "body_html") && c.SectionIndex >= 0 && c.SectionIndex < maxChunks && c.SectionStartOffset >= 0 && c.SectionStartOffset <= c.StartOffset && c.SectionEndOffset >= c.EndOffset && c.SectionEndOffset <= maxSnapshotBytes
}
func validCoverage(s string) bool { return s == "complete" || s == "partial" || s == "unavailable" }

// Replace performs no normalization or inference. The caller supplies an entire
// deterministic projection; old data remains visible until the transaction commits.
func (r *chunkRepository) Replace(ctx context.Context, input repository.ChunkSnapshot) (repository.IndexState, error) {
	var result repository.IndexState
	if input.AccountID == "" || input.EmailID == "" || !validHash(input.SourceRevision) ||
		!validName(input.PreprocessingVersion, 128) || !validName(input.IndexVersion, 128) || !validModel(input.Model) || len(input.Chunks) > maxChunks || input.ContentStatus != "" && !validCoverage(input.ContentStatus) {
		return result, repository.ErrInvalid
	}
	// Do not reorder or populate hashes in the caller's backing slice.
	chunks := append([]repository.ChunkInput(nil), input.Chunks...)
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Index < chunks[j].Index })
	total := 0
	for i := range chunks {
		if chunks[i].ContentHash == "" {
			chunks[i].ContentHash = hashText(chunks[i].Text)
		}
		total += len(chunks[i].Text)
		if chunks[i].Index != i || !validChunk(chunks[i]) || total > maxSnapshotBytes {
			return result, repository.ErrInvalid
		}
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		revision, body, err := chunkSource(ctx, tx, input.AccountID, input.EmailID)
		if err != nil {
			return err
		}
		if revision != input.SourceRevision {
			return repository.ErrConflict
		}
		coverage := input.ContentStatus
		if coverage == "" {
			coverage = "partial"
			if body == "complete" {
				coverage = "complete"
			}
		}
		if coverage == "complete" && body != "complete" {
			return repository.ErrInvalid
		}
		old, err := readIndexState(ctx, tx, input.AccountID, input.EmailID)
		hasOld := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		same := hasOld && old.Status != repository.IndexStale && old.SourceRevision == revision && old.PreprocessingVersion == input.PreprocessingVersion && old.IndexVersion == input.IndexVersion && old.Model == input.Model && old.BodyState == body && old.ContentStatus == coverage
		previous, err := readChunks(ctx, tx, input.AccountID, input.EmailID, 0, maxChunks)
		if err != nil {
			return err
		}
		if len(previous) != len(chunks) {
			same = false
		}
		// Match identical inputs at their original ordinal first, then reuse moved
		// chunks. This avoids re-embedding the tail when a new section is inserted.
		type cacheKey struct {
			hash, section, preprocessing, context string
			tokens                                int
		}
		key := func(c repository.ChunkInput, preprocessing string) cacheKey {
			return cacheKey{c.ContentHash, c.SectionType, preprocessing, c.ContextText, c.TokenCount}
		}
		byInput := make(map[cacheKey][]int, len(previous))
		for i, p := range previous {
			k := key(p.ChunkInput, p.PreprocessingVersion)
			byInput[k] = append(byInput[k], i)
		}
		matches := make([]int, len(chunks))
		used := make([]bool, len(previous))
		for i, c := range chunks {
			matches[i] = -1
			if i < len(previous) && previous[i].Index == i && key(previous[i].ChunkInput, previous[i].PreprocessingVersion) == key(c, input.PreprocessingVersion) && previous[i].Text == c.Text {
				matches[i] = i
				used[i] = true
			}
		}
		for i, c := range chunks {
			if matches[i] >= 0 {
				continue
			}
			k := key(c, input.PreprocessingVersion)
			for len(byInput[k]) > 0 {
				p := byInput[k][0]
				byInput[k] = byInput[k][1:]
				if !used[p] && previous[p].Text == c.Text {
					matches[i] = p
					used[p] = true
					break
				}
			}
		}
		for i, p := range previous {
			if !used[i] {
				same = false
				if _, err = tx.ExecContext(ctx, `DELETE FROM email_chunks WHERE account_id=? AND id=?`, input.AccountID, p.ID); err != nil {
					return err
				}
			}
		}
		// Move relocated rows outside the published ordinal range first, avoiding
		// uniqueness collisions for inserts/swaps. These positions never commit.
		for i, p := range matches {
			if p >= 0 && previous[p].Index != i {
				same = false
				if _, err = tx.ExecContext(ctx, `UPDATE email_chunks SET chunk_index=?,source_revision=? WHERE account_id=? AND id=?`, maxChunks+previous[p].Index, revision, input.AccountID, previous[p].ID); err != nil {
					return err
				}
			}
		}
		for i, c := range chunks {
			id := newID()
			if p := matches[i]; p >= 0 {
				id = previous[p].ID
				if previous[p].ChunkInput != c || previous[p].SourceRevision != revision {
					same = false
				}
				_, err = tx.ExecContext(ctx, `UPDATE email_chunks SET chunk_index=?,source_revision=?,start_offset=?,end_offset=?,source_kind=?,section_index=?,section_start_offset=?,section_end_offset=? WHERE account_id=? AND id=? AND (chunk_index<>? OR source_revision<>? OR start_offset<>? OR end_offset<>? OR source_kind<>? OR section_index<>? OR section_start_offset<>? OR section_end_offset<>?)`, c.Index, revision, c.StartOffset, c.EndOffset, c.SourceKind, c.SectionIndex, c.SectionStartOffset, c.SectionEndOffset, input.AccountID, id, c.Index, revision, c.StartOffset, c.EndOffset, c.SourceKind, c.SectionIndex, c.SectionStartOffset, c.SectionEndOffset)
			} else {
				same = false
				_, err = tx.ExecContext(ctx, `INSERT INTO email_chunks(id,account_id,email_id,chunk_index,section_type,text,token_count,source_revision,start_offset,end_offset,content_hash,preprocessing_version,source_kind,section_index,section_start_offset,section_end_offset,context_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, input.AccountID, input.EmailID, c.Index, c.SectionType, c.Text, c.TokenCount, revision, c.StartOffset, c.EndOffset, c.ContentHash, input.PreprocessingVersion, c.SourceKind, c.SectionIndex, c.SectionStartOffset, c.SectionEndOffset, c.ContextText)
			}
			if err != nil {
				return err
			}
			// A model recipe cannot change dimension without changing its version.
			var dimension int
			err = tx.QueryRowContext(ctx, `SELECT dimension FROM chunk_embeddings WHERE account_id=? AND chunk_id=? AND model_id=? AND model_version=?`, input.AccountID, id, input.Model.ID, input.Model.Version).Scan(&dimension)
			if err == nil && dimension != input.Model.Dimension {
				return repository.ErrConflict
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO chunk_embeddings(account_id,chunk_id,model_id,model_version,dimension,status) VALUES(?,?,?,?,?,'pending') ON CONFLICT DO NOTHING`, input.AccountID, id, input.Model.ID, input.Model.Version, input.Model.Dimension)
			if err != nil {
				return err
			}
		}
		result = repository.IndexState{AccountID: input.AccountID, EmailID: input.EmailID, SourceRevision: revision, PreprocessingVersion: input.PreprocessingVersion, IndexVersion: input.IndexVersion, Model: input.Model, Generation: newID(), BodyState: body, ContentStatus: coverage}
		if same {
			result.Generation = old.Generation
			result.LastIndexedAt = old.LastIndexedAt
		}
		if err = refreshIndexStatus(ctx, tx, &result); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO index_state(account_id,email_id,source_revision,preprocessing_version,index_version,model_id,model_version,dimension,generation,body_state,status,last_indexed_at,content_status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,email_id) DO UPDATE SET source_revision=excluded.source_revision,preprocessing_version=excluded.preprocessing_version,index_version=excluded.index_version,model_id=excluded.model_id,model_version=excluded.model_version,dimension=excluded.dimension,generation=excluded.generation,body_state=excluded.body_state,status=excluded.status,last_indexed_at=excluded.last_indexed_at,content_status=excluded.content_status`, result.AccountID, result.EmailID, result.SourceRevision, result.PreprocessingVersion, result.IndexVersion, result.Model.ID, result.Model.Version, result.Model.Dimension, result.Generation, result.BodyState, result.Status, timeValue(result), result.ContentStatus)
		return err
	})
	if err != nil {
		return repository.IndexState{}, err
	}
	return result, nil
}

func timeValue(s repository.IndexState) any {
	if s.LastIndexedAt == nil {
		return nil
	}
	return s.LastIndexedAt.UnixMilli()
}
func chunkSource(ctx context.Context, tx *sql.Tx, a domain.AccountID, e domain.EmailID) (string, string, error) {
	var revision, body string
	err := tx.QueryRowContext(ctx, `SELECT content_hash,body_state FROM emails WHERE account_id=? AND id=? AND remote_deleted=0`, a, e).Scan(&revision, &body)
	return revision, body, err
}
func readIndexState(ctx context.Context, tx *sql.Tx, a domain.AccountID, e domain.EmailID) (repository.IndexState, error) {
	var s repository.IndexState
	var last sql.NullInt64
	s.AccountID = a
	s.EmailID = e
	err := tx.QueryRowContext(ctx, `SELECT source_revision,preprocessing_version,index_version,model_id,model_version,dimension,generation,body_state,status,last_indexed_at,content_status FROM index_state WHERE account_id=? AND email_id=?`, a, e).Scan(&s.SourceRevision, &s.PreprocessingVersion, &s.IndexVersion, &s.Model.ID, &s.Model.Version, &s.Model.Dimension, &s.Generation, &s.BodyState, &s.Status, &last, &s.ContentStatus)
	s.LastIndexedAt = optionalTime(last)
	return s, err
}
func currentIndex(ctx context.Context, tx *sql.Tx, a domain.AccountID, e domain.EmailID) (repository.IndexState, error) {
	revision, body, err := chunkSource(ctx, tx, a, e)
	if err != nil {
		return repository.IndexState{}, err
	}
	s, err := readIndexState(ctx, tx, a, e)
	if err != nil {
		return s, err
	}
	if s.Status == repository.IndexStale || s.SourceRevision != revision || s.BodyState != body {
		return s, repository.ErrConflict
	}
	return s, nil
}
func (r *chunkRepository) State(ctx context.Context, a domain.AccountID, e domain.EmailID) (repository.IndexState, error) {
	var s repository.IndexState
	if a == "" || e == "" {
		return s, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if _, _, err := chunkSource(ctx, tx, a, e); err != nil {
			return err
		}
		var err error
		s, err = readIndexState(ctx, tx, a, e)
		return err
	})
	if err != nil {
		return repository.IndexState{}, err
	}
	return s, nil
}
func readChunks(ctx context.Context, tx *sql.Tx, a domain.AccountID, e domain.EmailID, offset, limit int) ([]repository.EmailChunk, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,chunk_index,section_type,text,token_count,source_revision,start_offset,end_offset,content_hash,preprocessing_version,source_kind,section_index,section_start_offset,section_end_offset,context_text FROM email_chunks WHERE account_id=? AND email_id=? ORDER BY chunk_index LIMIT ? OFFSET ?`, a, e, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []repository.EmailChunk{}
	for rows.Next() {
		c := repository.EmailChunk{AccountID: a, EmailID: e}
		if err := rows.Scan(&c.ID, &c.Index, &c.SectionType, &c.Text, &c.TokenCount, &c.SourceRevision, &c.StartOffset, &c.EndOffset, &c.ContentHash, &c.PreprocessingVersion, &c.SourceKind, &c.SectionIndex, &c.SectionStartOffset, &c.SectionEndOffset, &c.ContextText); err != nil {
			return nil, err
		}
		if !validChunk(c.ChunkInput) || !validHash(c.SourceRevision) || !validName(c.PreprocessingVersion, 128) {
			return nil, repository.ErrStorage
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (r *chunkRepository) List(ctx context.Context, a domain.AccountID, e domain.EmailID, page repository.Page) ([]repository.EmailChunk, error) {
	if a == "" || e == "" || page.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	var chunks []repository.EmailChunk
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		s, err := currentIndex(ctx, tx, a, e)
		if err != nil {
			return err
		}
		chunks, err = readChunks(ctx, tx, a, e, page.Offset, page.Limit)
		if err != nil {
			return err
		}
		for _, c := range chunks {
			if c.SourceRevision != s.SourceRevision || c.PreprocessingVersion != s.PreprocessingVersion {
				return repository.ErrConflict
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func encodeVector(v []float32, dimension int) ([]byte, error) {
	if len(v) != dimension || dimension < 1 || dimension > 8192 {
		return nil, repository.ErrInvalid
	}
	data := make([]byte, 4*dimension)
	var norm float64
	for i, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nil, repository.ErrInvalid
		}
		norm += float64(f) * float64(f)
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(f))
	}
	if norm == 0 {
		return nil, repository.ErrInvalid
	}
	return data, nil
}
func decodeVector(data []byte, dimension int) ([]float32, error) {
	if dimension < 1 || dimension > 8192 || len(data) != dimension*4 {
		return nil, repository.ErrStorage
	}
	v := make([]float32, dimension)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	if _, err := encodeVector(v, dimension); err != nil {
		return nil, repository.ErrStorage
	}
	return v, nil
}
func refreshIndexStatus(ctx context.Context, tx *sql.Tx, s *repository.IndexState) error {
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(v.dimension,?),COALESCE(v.status,'pending'),v.vector_data FROM email_chunks c LEFT JOIN chunk_embeddings v ON v.account_id=c.account_id AND v.chunk_id=c.id AND v.model_id=? AND v.model_version=? WHERE c.account_id=? AND c.email_id=? AND c.source_revision=? AND c.preprocessing_version=? ORDER BY c.chunk_index`, s.Model.Dimension, s.Model.ID, s.Model.Version, s.AccountID, s.EmailID, s.SourceRevision, s.PreprocessingVersion)
	if err != nil {
		return err
	}
	defer rows.Close()
	total, ready := 0, 0
	for rows.Next() {
		var dim int
		var status string
		var data []byte
		if err := rows.Scan(&dim, &status, &data); err != nil {
			return err
		}
		total++
		if dim != s.Model.Dimension {
			return repository.ErrStorage
		}
		if status == repository.EmbeddingReady {
			if _, err := decodeVector(data, dim); err != nil {
				return err
			}
			ready++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.Status = repository.IndexPending
	if total == 0 {
		s.Status = repository.IndexUnavailable
	} else if s.BodyState != "complete" || s.ContentStatus != "complete" || ready > 0 && ready < total {
		s.Status = repository.IndexPartial
	} else if ready == total {
		s.Status = repository.IndexReady
	}
	if total > 0 && ready == total {
		if s.LastIndexedAt == nil {
			t := instant(nowMillis())
			s.LastIndexedAt = &t
		}
	} else {
		s.LastIndexedAt = nil
	}
	return nil
}
func (r *chunkRepository) SaveEmbeddings(ctx context.Context, b repository.EmbeddingBatch) (repository.IndexState, error) {
	s := b.State
	if s.AccountID == "" || s.EmailID == "" || !validHash(s.SourceRevision) || !validName(s.Generation, 128) || !validModel(s.Model) || len(b.Embeddings) == 0 || len(b.Embeddings) > maxEmbeddingBatch {
		return repository.IndexState{}, repository.ErrInvalid
	}
	encoded := make([][]byte, len(b.Embeddings))
	seen := map[string]bool{}
	for i, e := range b.Embeddings {
		if !validName(e.ChunkID, 128) || seen[e.ChunkID] {
			return repository.IndexState{}, repository.ErrInvalid
		}
		seen[e.ChunkID] = true
		switch e.Status {
		case repository.EmbeddingReady:
			var err error
			encoded[i], err = encodeVector(e.Vector, s.Model.Dimension)
			if err != nil {
				return repository.IndexState{}, err
			}
		case repository.EmbeddingPending, repository.EmbeddingFailed:
			if len(e.Vector) != 0 {
				return repository.IndexState{}, repository.ErrInvalid
			}
		default:
			return repository.IndexState{}, repository.ErrInvalid
		}
	}
	var result repository.IndexState
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		current, err := currentIndex(ctx, tx, s.AccountID, s.EmailID)
		if err != nil {
			return err
		}
		if current.Generation != s.Generation || current.SourceRevision != s.SourceRevision || current.Model != s.Model {
			return repository.ErrConflict
		}
		for i, e := range b.Embeddings {
			if err := exists(ctx, tx, `SELECT 1 FROM email_chunks WHERE account_id=? AND email_id=? AND id=? AND source_revision=? AND preprocessing_version=?`, s.AccountID, s.EmailID, e.ChunkID, s.SourceRevision, current.PreprocessingVersion); err != nil {
				return err
			}
			write, err := tx.ExecContext(ctx, `INSERT INTO chunk_embeddings(account_id,chunk_id,model_id,model_version,dimension,vector_data,status) VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id,chunk_id,model_id,model_version) DO UPDATE SET dimension=excluded.dimension,vector_data=excluded.vector_data,status=excluded.status WHERE dimension<>excluded.dimension OR vector_data IS NOT excluded.vector_data OR status<>excluded.status`, s.AccountID, e.ChunkID, s.Model.ID, s.Model.Version, s.Model.Dimension, encoded[i], e.Status)
			if err != nil {
				return err
			}
			changed, err := write.RowsAffected()
			if err != nil {
				return err
			}
			if changed > 0 {
				current.LastIndexedAt = nil
			}
		}
		if err := refreshIndexStatus(ctx, tx, &current); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE index_state SET status=?,last_indexed_at=? WHERE account_id=? AND email_id=?`, current.Status, timeValue(current), s.AccountID, s.EmailID)
		result = current
		return err
	})
	if err != nil {
		return repository.IndexState{}, err
	}
	return result, nil
}
func (r *chunkRepository) Embedding(ctx context.Context, a domain.AccountID, e domain.EmailID, id string, model repository.EmbeddingModel) (repository.ChunkEmbedding, error) {
	var result repository.ChunkEmbedding
	if a == "" || e == "" || !validName(id, 128) || !validModel(model) {
		return result, repository.ErrInvalid
	}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		s, err := currentIndex(ctx, tx, a, e)
		if err != nil {
			return err
		}
		if s.Model != model {
			return repository.ErrConflict
		}
		var dimension int
		var data []byte
		result.ChunkID = id
		err = tx.QueryRowContext(ctx, `SELECT v.dimension,v.status,v.vector_data FROM chunk_embeddings v JOIN email_chunks c ON c.account_id=v.account_id AND c.id=v.chunk_id WHERE c.account_id=? AND c.email_id=? AND c.id=? AND c.source_revision=? AND c.preprocessing_version=? AND v.model_id=? AND v.model_version=?`, a, e, id, s.SourceRevision, s.PreprocessingVersion, model.ID, model.Version).Scan(&dimension, &result.Status, &data)
		if err != nil {
			return err
		}
		if dimension != model.Dimension {
			return repository.ErrStorage
		}
		if result.Status == repository.EmbeddingReady {
			result.Vector, err = decodeVector(data, dimension)
			return err
		}
		if (result.Status != repository.EmbeddingPending && result.Status != repository.EmbeddingFailed) || data != nil {
			return repository.ErrStorage
		}
		return nil
	})
	if err != nil {
		return repository.ChunkEmbedding{}, err
	}
	return result, nil
}
