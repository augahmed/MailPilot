package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/search"
	"math"
)

type passageStore struct{ store *Store }

func (s *Store) PassageVectors() search.PassageVectors { return &passageStore{s} }
func decodePassage(data []byte, dimension int) []float32 {
	if len(data) != dimension*4 {
		return nil
	}
	v := make([]float32, dimension)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	if embedding.ValidateVector(v, dimension) != nil {
		return nil
	}
	return v
}
func passageCache(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, a domain.AccountID, id domain.EmailID, m search.Model) (map[string][]float32, error) {
	rows, err := db.QueryContext(ctx, `SELECT input_hash,vector FROM email_passage_vectors WHERE account_id=? AND email_id=? AND provider=? AND dimension=? AND preprocessing_version=?`, a, id, m.Provider, m.Dimension, m.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]float32{}
	for rows.Next() {
		var hash string
		var data []byte
		if err := rows.Scan(&hash, &data); err != nil {
			return nil, err
		}
		if v := decodePassage(data, m.Dimension); v != nil {
			result[hash] = v
		}
	}
	return result, rows.Err()
}
func (r *passageStore) Cached(ctx context.Context, a domain.AccountID, id domain.EmailID, m search.Model) (map[string][]float32, error) {
	if a == "" || id == "" || m.Validate() != nil || m.Version != embedding.RetrievalVersion {
		return nil, repository.ErrInvalid
	}
	result, err := passageCache(ctx, r.store.db, a, id, m)
	return result, storageError(err)
}
func (r *passageStore) Replace(ctx context.Context, source repository.StoredEmail, m search.Model, prepared embedding.RetrievalText, values map[string][]float32) error {
	if m.Validate() != nil || m.Version != embedding.RetrievalVersion || len(prepared.Passages) > embedding.MaxPassages {
		return repository.ErrInvalid
	}
	for _, p := range prepared.Passages {
		if embedding.ValidateVector(values[p.Hash], m.Dimension) != nil {
			return embedding.ErrVector
		}
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		a, id := source.Email.AccountID, source.Email.ID
		if err := checkInputHash(ctx, tx, a, id, source.ContentHash); err != nil {
			return err
		}
		current, err := scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE remote_deleted=0 AND account_id=? AND id=?", a, id))
		if err != nil {
			return err
		}
		fresh, err := embedding.PrepareRetrieval(current.Email)
		if err != nil || fresh.Hash != prepared.Hash || len(fresh.Passages) != len(prepared.Passages) {
			return repository.ErrConflict
		}
		for i, p := range fresh.Passages {
			if p != prepared.Passages[i] {
				return repository.ErrConflict
			}
		}
		seen := map[string]bool{}
		for _, p := range fresh.Passages {
			if seen[p.Hash] {
				continue
			}
			seen[p.Hash] = true
			v := values[p.Hash]
			data := make([]byte, m.Dimension*4)
			for i, f := range v {
				binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(f))
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO email_passage_vectors VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id,email_id,provider,dimension,preprocessing_version,input_hash) DO UPDATE SET vector=excluded.vector`, a, id, m.Provider, m.Dimension, m.Version, p.Hash, data); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO email_retrieval_indexes VALUES(?,?,?,?,?,?) ON CONFLICT(account_id,email_id,provider,dimension,preprocessing_version) DO UPDATE SET input_hash=excluded.input_hash`, a, id, m.Provider, m.Dimension, m.Version, fresh.Hash)
		return err
	})
}

// Keep unchanged normalized passages through edits; remove stale manifests and
// obsolete vectors in the same transaction as the source update.
func invalidatePassages(ctx context.Context, tx *sql.Tx, e domain.Email, id domain.EmailID) error {
	prepared, err := embedding.PrepareRetrieval(e)
	hash := ""
	hashes := []string{}
	if err == nil {
		hash = prepared.Hash
		for _, p := range prepared.Passages {
			hashes = append(hashes, p.Hash)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM email_retrieval_indexes WHERE account_id=? AND email_id=? AND (input_hash<>? OR preprocessing_version<>?)", e.AccountID, id, hash, embedding.RetrievalVersion); err != nil {
		return err
	}
	raw, _ := json.Marshal(hashes)
	_, err = tx.ExecContext(ctx, `DELETE FROM email_passage_vectors WHERE account_id=? AND email_id=? AND (preprocessing_version<>? OR input_hash NOT IN (SELECT value FROM json_each(?)))`, e.AccountID, id, embedding.RetrievalVersion, string(raw))
	return err
}
