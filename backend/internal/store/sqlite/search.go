package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func (s *Store) Vectors() repository.VectorRepository { return &vectorRepository{s} }

type vectorRepository struct{ store *Store }

// Every join includes account ownership. A ready message requires all chunks to
// have completed; old model/preprocessing generations and incomplete bodies stay
// excluded. Fingerprints are pinned by the existing local index model registry.
func vectorQuery(a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter) (string, []any) {
	q := ` FROM email_chunks c
 JOIN emails e ON e.account_id=c.account_id AND e.id=c.email_id
 JOIN index_state s ON s.account_id=c.account_id AND s.email_id=c.email_id
 JOIN chunk_embeddings v ON v.account_id=c.account_id AND v.chunk_id=c.id
 JOIN local_index_models m ON m.account_id=c.account_id AND m.model_id=s.model_id AND m.model_version=s.model_version
 JOIN email_addresses a ON a.email_id=e.id AND a.role='sender' AND a.position=0
 WHERE c.account_id=? AND e.remote_deleted=0 AND s.status='ready'
 AND s.body_state='complete' AND s.content_status='complete' AND s.last_indexed_at IS NOT NULL
 AND s.source_revision=e.content_hash AND c.source_revision=e.content_hash
 AND c.preprocessing_version=s.preprocessing_version AND s.preprocessing_version=? AND s.index_version=?
 AND s.model_id=? AND s.model_version=? AND s.dimension=?
 AND m.dimension=s.dimension AND m.fingerprint=?
 AND v.model_id=s.model_id AND v.model_version=s.model_version AND v.dimension=s.dimension AND v.status='ready'`
	args := []any{a, p.PreprocessingVersion, p.IndexVersion, p.Model.ID, p.Model.Version, p.Model.Dimension, p.Fingerprint}
	if f.Sender != "" {
		q += ` AND (instr(lower(a.address),lower(?))>0 OR instr(lower(a.name),lower(?))>0)`
		args = append(args, f.Sender, f.Sender)
	}
	if f.After != 0 {
		q += ` AND e.received_at>=?`
		args = append(args, f.After)
	}
	if f.Before != 0 {
		q += ` AND e.received_at<?`
		args = append(args, f.Before)
	}
	if f.CategoryID != "" {
		q += ` AND EXISTS(SELECT 1 FROM email_categories ec WHERE ec.account_id=e.account_id AND ec.email_id=e.id AND ec.category_id=?)`
		args = append(args, f.CategoryID)
	}
	return q, args
}
func validSearchProfile(a domain.AccountID, p repository.IndexProfile) bool {
	return a != "" && validModel(p.Model) && validHash(p.Fingerprint) && validName(p.PreprocessingVersion, 128) && validName(p.IndexVersion, 128)
}
func (r *vectorRepository) ScanVectors(ctx context.Context, a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter, visit func(repository.VectorSource) error) error {
	if !validSearchProfile(a, p) || visit == nil {
		return repository.ErrInvalid
	}
	q, args := vectorQuery(a, p, f)
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.email_id,c.chunk_index,c.section_type,c.text,c.token_count,c.source_revision,c.start_offset,c.end_offset,c.content_hash,c.preprocessing_version,c.source_kind,c.section_index,c.section_start_offset,c.section_end_offset,c.context_text,s.generation,e.subject,a.name,a.address,e.received_at,v.vector_data`+q+` ORDER BY c.email_id,c.chunk_index,c.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		vector := make([]float32, p.Model.Dimension)
		for rows.Next() {
			if err = ctx.Err(); err != nil {
				return err
			}
			var v repository.VectorSource
			c := &v.Chunk
			c.AccountID = a
			var blob []byte
			if err = rows.Scan(&c.ID, &c.EmailID, &c.Index, &c.SectionType, &c.Text, &c.TokenCount, &c.SourceRevision, &c.StartOffset, &c.EndOffset, &c.ContentHash, &c.PreprocessingVersion, &c.SourceKind, &c.SectionIndex, &c.SectionStartOffset, &c.SectionEndOffset, &c.ContextText, &v.Generation, &v.Subject, &v.SenderName, &v.SenderAddress, &v.ReceivedAt, &blob); err != nil {
				return err
			}
			if !validChunk(c.ChunkInput) || !validHash(c.SourceRevision) {
				return repository.ErrStorage
			}
			if len(blob) != len(vector)*4 {
				return repository.ErrStorage
			}
			var squaredNorm float64
			for i := range vector {
				x := math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
					return repository.ErrStorage
				}
				vector[i] = x
				squaredNorm += float64(x) * float64(x)
			}
			if squaredNorm == 0 {
				return repository.ErrStorage
			}
			v.Vector = vector

			if err = visit(v); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}
func (r *vectorRepository) CheckVectorSources(ctx context.Context, a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter, sources []repository.VectorSource) error {
	if !validSearchProfile(a, p) || len(sources) > 60 {
		return repository.ErrInvalid
	}
	q, args := vectorQuery(a, p, f)
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		for _, v := range sources {
			if v.Chunk.AccountID != a {
				return repository.ErrConflict
			}
			var n int
			bound := append(append([]any{}, args...), v.Chunk.ID, v.Chunk.SourceRevision, v.Generation)
			err := tx.QueryRowContext(ctx, `SELECT count(*)`+q+` AND c.id=? AND c.source_revision=? AND s.generation=?`, bound...).Scan(&n)
			if err != nil {
				return err
			}
			if n != 1 {
				return repository.ErrConflict
			}
		}
		return nil
	})
}
