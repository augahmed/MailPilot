package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"

	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

func (s *Store) CategoryVectors() embedding.CategoryVectorStore { return &vectorStore{s} }
func (r *vectorStore) GetCategory(ctx context.Context, k embedding.CategoryKey) ([]float32, error) {
	if k.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	var data []byte
	err := r.store.db.QueryRowContext(ctx, `SELECT v.vector FROM category_embeddings v JOIN categories c ON c.account_id=v.account_id AND c.id=v.category_id WHERE v.account_id=? AND v.category_id=? AND v.provider=? AND v.dimension=? AND v.preprocessing_version=? AND v.input_hash=? AND c.enabled=1`, k.AccountID, k.CategoryID, k.Provider, k.Dimension, k.Version, k.InputHash).Scan(&data)
	if err != nil {
		return nil, storageError(err)
	}
	if len(data) != k.Dimension*4 {
		return nil, repository.ErrStorage
	}
	v := make([]float32, k.Dimension)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	if embedding.ValidateVector(v, k.Dimension) != nil {
		return nil, repository.ErrStorage
	}
	return v, nil
}
func (r *vectorStore) PutCategories(ctx context.Context, records []embedding.CategoryRecord) error {
	if len(records) == 0 || len(records) > 1000 {
		return repository.ErrInvalid
	}
	for _, record := range records {
		if record.Key.Validate() != nil || embedding.ValidateVector(record.Vector, record.Key.Dimension) != nil {
			return repository.ErrInvalid
		}
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		for _, record := range records {
			k := record.Key
			c, err := scanCategory(tx.QueryRowContext(ctx, "SELECT id,name,description,enabled,created_at,updated_at FROM categories WHERE account_id=? AND id=?", k.AccountID, k.CategoryID))
			if err != nil {
				return err
			}
			prepared, err := embedding.PrepareCategory(c)
			if err != nil || prepared.Hash != k.InputHash {
				return repository.ErrConflict
			}
			data := make([]byte, 4*len(record.Vector))
			for i, f := range record.Vector {
				binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(f))
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO category_embeddings VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id,category_id,provider,dimension,preprocessing_version) DO UPDATE SET input_hash=excluded.input_hash,vector=excluded.vector`, k.AccountID, k.CategoryID, k.Provider, k.Dimension, k.Version, k.InputHash, data)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
