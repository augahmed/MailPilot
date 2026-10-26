// Package sqlite owns the application's local database and embedded migrations.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db      *sql.DB
	version int
}

func Open(ctx context.Context, dataDir string) (*Store, error) {
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("data directory must be absolute")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Lstat(dataDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("data directory must be a real directory")
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := prepareDatabaseFiles(dataDir); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "mailpilot.db")
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	// Apply on every connection, including replacements opened by database/sql.
	// Memory-only temporary tables/sorts reduce plaintext query spills. Secure
	// delete reduces ordinary deleted-row remnants; neither setting encrypts the
	// database or guarantees erasure of FTS, WAL, filesystem snapshots or backups.
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "temp_store(MEMORY)", "secure_delete(ON)", "journal_mode(WAL)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(err error) (*Store, error) { _ = db.Close(); return nil, err }
	if err := db.PingContext(ctx); err != nil {
		return fail(err)
	}
	version, err := migrate(ctx, db)
	if err != nil {
		return fail(err)
	}
	return &Store{db: db, version: version}, nil
}

func migrate(ctx context.Context, db *sql.DB) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return 0, err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	var latest, count int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0),COUNT(*) FROM schema_migrations").Scan(&latest, &count); err != nil {
		return 0, err
	}
	if latest > len(entries) {
		return 0, errors.New("database schema is newer than this application")
	}
	if latest < 0 || count != latest {
		return 0, errors.New("migration history is incomplete")
	}
	for index, entry := range entries {
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version != index+1 {
			return 0, errors.New("invalid migration sequence")
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return 0, err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		var stored string
		err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = ?", version).Scan(&stored)
		switch {
		case err == nil:
			if stored != checksum {
				return 0, errors.New("migration checksum mismatch")
			}
		case errors.Is(err, sql.ErrNoRows):
			if version <= latest {
				return 0, errors.New("migration history is incomplete")
			}
			if _, err = tx.ExecContext(ctx, string(body)); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?,?,?)", version, checksum, time.Now().UTC().UnixMilli()); err != nil {
				return 0, err
			}
		default:
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (s *Store) Ping(ctx context.Context) error {
	var value int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
		return err
	}
	if value != 1 {
		return errors.New("database probe failed")
	}
	return nil
}
func (s *Store) SchemaVersion() int { return s.version }
func (s *Store) Close() error       { return s.db.Close() }
