package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

// Keyword visibility depends on current shared text, never vector readiness or
// model availability. Partial cached projections are usable and explicitly marked.
func keywordQuery(a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter) (string, []any) {
	q := ` FROM email_chunks c
 JOIN emails e ON e.account_id=c.account_id AND e.id=c.email_id
 JOIN index_state s ON s.account_id=c.account_id AND s.email_id=c.email_id
 JOIN email_addresses a ON a.email_id=e.id AND a.role='sender' AND a.position=0
 WHERE e.account_id=? AND e.remote_deleted=0
 AND s.status<>'stale' AND c.source_revision=e.content_hash AND s.source_revision=e.content_hash
 AND c.preprocessing_version=? AND s.preprocessing_version=c.preprocessing_version AND s.index_version=?`
	return searchFilters(q, []any{a, p.PreprocessingVersion, p.IndexVersion}, f)
}

func (r *vectorRepository) ScanKeywords(ctx context.Context, a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter, query repository.KeywordQuery, visit func(repository.VectorSource, float64) error) error {
	if !validSearchProfile(a, p) || visit == nil || len(query.Match) > 16384 || len(query.Literals) > 128 {
		return repository.ErrInvalid
	}
	q, args := keywordQuery(a, p, f)
	// FTS supplies matches only. Global BM25 statistics would leak other accounts'
	// retained text through scores/ranks; retrieval scores each passage locally.
	cte := `WITH matches AS MATERIALIZED (SELECT chunk_fts.rowid FROM chunk_fts JOIN email_chunks indexed_chunk ON indexed_chunk.rowid=chunk_fts.rowid WHERE chunk_fts MATCH ? AND indexed_chunk.account_id=?)`
	if query.Match == "" {
		cte = `WITH matches AS MATERIALIZED (SELECT rowid FROM chunk_fts WHERE 0 AND ?='' AND ?='')`
	}
	q = strings.Replace(q, " WHERE e.account_id", " LEFT JOIN matches ON matches.rowid=c.rowid WHERE e.account_id", 1)
	q += ` AND (matches.rowid IS NOT NULL`
	for _, literal := range query.Literals {
		q += ` OR instr(lower(a.name),lower(?))>0 OR instr(lower(a.address),lower(?))>0`
		args = append(args, literal, literal)
	}
	q += `)`
	args = append([]any{query.Match, a}, args...)
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, cte+` SELECT c.id,c.email_id,c.chunk_index,c.section_type,c.text,c.token_count,c.source_revision,c.start_offset,c.end_offset,c.content_hash,c.preprocessing_version,c.source_kind,c.section_index,c.section_start_offset,c.section_end_offset,c.context_text,s.generation,e.subject,a.name,a.address,e.received_at,(s.body_state<>'complete' OR s.content_status<>'complete'),0.0`+q+` ORDER BY c.email_id,c.chunk_index,c.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err = ctx.Err(); err != nil {
				return err
			}
			v := repository.VectorSource{Keyword: true}
			c := &v.Chunk
			c.AccountID = a
			var score float64
			if err = rows.Scan(&c.ID, &c.EmailID, &c.Index, &c.SectionType, &c.Text, &c.TokenCount, &c.SourceRevision, &c.StartOffset, &c.EndOffset, &c.ContentHash, &c.PreprocessingVersion, &c.SourceKind, &c.SectionIndex, &c.SectionStartOffset, &c.SectionEndOffset, &c.ContextText, &v.Generation, &v.Subject, &v.SenderName, &v.SenderAddress, &v.ReceivedAt, &v.Partial, &score); err != nil {
				return err
			}
			if !validChunk(c.ChunkInput) || !validHash(c.SourceRevision) {
				return repository.ErrStorage
			}
			if err = visit(v, score); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

func (r *vectorRepository) CheckKeywordSources(ctx context.Context, a domain.AccountID, p repository.IndexProfile, f repository.SearchFilter, sources []repository.VectorSource) error {
	if !validSearchProfile(a, p) || len(sources) > 300 {
		return repository.ErrInvalid
	}
	q, args := keywordQuery(a, p, f)
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		for _, v := range sources {
			if v.Chunk.AccountID != a {
				return repository.ErrConflict
			}
			var n int
			bound := append(append([]any{}, args...), v.Chunk.ID, v.Chunk.SourceRevision, v.Generation)
			if err := tx.QueryRowContext(ctx, `SELECT count(*)`+q+` AND c.id=? AND c.source_revision=? AND s.generation=?`, bound...).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return repository.ErrConflict
			}
		}
		return nil
	})
}

func (r *vectorRepository) KeywordCoverage(ctx context.Context, a domain.AccountID, p repository.IndexProfile) (int, error) {
	if !validSearchProfile(a, p) {
		return 0, repository.ErrInvalid
	}
	q, args := keywordQuery(a, p, repository.SearchFilter{})
	var count int
	err := r.store.db.QueryRowContext(ctx, `SELECT count(DISTINCT c.email_id)`+q, args...).Scan(&count)
	return count, err
}
