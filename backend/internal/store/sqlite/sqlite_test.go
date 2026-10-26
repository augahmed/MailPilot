package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInitializeAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "private data #1")
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion() != 21 {
		t.Fatal("unexpected schema version")
	}
	for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2, "temp_store": 2, "secure_delete": 1} {
		var got int
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d, %v", pragma, got, err)
		}
	}
	var journal string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal: %s, %v", journal, err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE VIRTUAL TABLE temp.fts_probe USING fts5(body)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings VALUES ('theme','"system"',0)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("settings did not persist: %d, %v", count, err)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "mailpilot.db"): 0600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode {
				t.Fatalf("unexpected permissions: %v", info.Mode())
			}
		}
	}
}

func TestRejectsTamperedOrFutureSchema(t *testing.T) {
	for _, statement := range []string{`UPDATE schema_migrations SET checksum='tampered'`, `INSERT INTO schema_migrations VALUES (999,'future',0)`} {
		t.Run(statement, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			s, err := Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			if s, err := Open(ctx, dir); err == nil {
				_ = s.Close()
				t.Fatal("expected schema rejection")
			}
		})
	}
}

func TestInitializationFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE schema_migrations"); err != nil {
		t.Fatal(err)
	}
	// Existing settings makes the migration fail. Its history table must roll back.
	if _, err := migrate(ctx, s.db); err == nil {
		t.Fatal("expected migration failure")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='schema_migrations'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("migration was not atomic: %d, %v", count, err)
	}
	_ = s.Close()
}

func TestRejectsInvalidStorage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mailpilot.db"), []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(context.Background(), dir); err == nil {
		_ = s.Close()
		t.Fatal("accepted corrupt database")
	}
	if s, err := Open(context.Background(), "relative"); err == nil {
		_ = s.Close()
		t.Fatal("accepted relative directory")
	}
}
