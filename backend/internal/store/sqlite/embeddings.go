package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"

	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

type vectorStore struct{ store *Store }

func (s *Store) Vectors() embedding.VectorStore { return &vectorStore{s} }

func (r *vectorStore) Get(ctx context.Context, k embedding.Key) ([]float32, error) {
	if k.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	var data []byte
	err := r.store.db.QueryRowContext(ctx, `SELECT vector FROM email_embeddings WHERE account_id=? AND email_id=? AND provider=? AND dimension=? AND preprocessing_version=? AND input_hash=?`, k.AccountID, k.EmailID, k.Provider, k.Dimension, k.Version, k.InputHash).Scan(&data)
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

func (r *vectorStore) PutBatch(ctx context.Context, records []embedding.Record) error {
	if len(records) == 0 || len(records) > embedding.MaxBatchSize {
		return repository.ErrInvalid
	}
	for _, record := range records {
		if record.Key.Validate() != nil || record.SourceHash == "" || embedding.ValidateVector(record.Vector, record.Key.Dimension) != nil {
			return repository.ErrInvalid
		}
	}
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		for _, record := range records {
			k := record.Key
			if err := checkInputHash(ctx, tx, k.AccountID, k.EmailID, record.SourceHash); err != nil {
				return err
			}
			// Recheck the exact cleaned payload, not just a caller-supplied hash.
			source, err := scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE account_id=? AND id=?", k.AccountID, k.EmailID))
			if err != nil {
				return err
			}
			if err := loadEmailRelations(ctx, tx, &source.Email); err != nil {
				return err
			}
			prepared, err := embedding.Prepare(source.Email)
			if err != nil || prepared.Hash != k.InputHash {
				return repository.ErrConflict
			}
			data := make([]byte, 4*len(record.Vector))
			for i, f := range record.Vector {
				binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(f))
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO email_embeddings VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id,email_id,provider,dimension,preprocessing_version) DO UPDATE SET input_hash=excluded.input_hash,vector=excluded.vector`, k.AccountID, k.EmailID, k.Provider, k.Dimension, k.Version, k.InputHash, data)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
