package sqlite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStorageFilesRepairPermissionsWithoutChangingContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission qualification")
	}
	dir := t.TempDir()
	content := []byte("synthetic private storage")
	names := []string{"mailpilot.db", "mailpilot.db-wal", "mailpilot.db-shm", "mailpilot.db-journal"}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0666); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareDatabaseFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s did not become private: %v", name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("%s content changed: %v", name, err)
		}
	}
}

func TestOpenRejectsUnsafeDatabaseFilesBeforeSQLiteReadsThem(t *testing.T) {
	for _, name := range []string{"mailpilot.db", "mailpilot.db-wal", "mailpilot.db-shm", "mailpilot.db-journal"} {
		for _, kind := range []string{"symlink", "hardlink", "directory"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				if kind == "hardlink" && runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
					t.Skip("hardlink qualification on macOS/Linux")
				}
				dir := t.TempDir()
				target := filepath.Join(t.TempDir(), "unrelated-file")
				content := []byte("synthetic unrelated private contents")
				if err := os.WriteFile(target, content, 0644); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, name)
				switch kind {
				case "symlink":
					err = os.Symlink(target, path)
				case "hardlink":
					err = os.Link(target, path)
				case "directory":
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					if runtime.GOOS == "windows" && kind == "symlink" {
						t.Skip("Windows symlink privileges unavailable")
					}
					t.Fatal(err)
				}
				store, err := Open(context.Background(), dir)
				if err == nil {
					_ = store.Close()
					t.Fatal("accepted an unsafe database file")
				}
				got, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(got, content) {
					t.Fatal("unrelated file was modified", err)
				}
				after, err := os.Stat(target)
				if err != nil || before.Mode() != after.Mode() {
					t.Fatal("unrelated file permissions were changed", err)
				}
				if name != "mailpilot.db" {
					if _, err := os.Stat(filepath.Join(dir, "mailpilot.db")); !os.IsNotExist(err) {
						t.Fatal("created database before rejecting unsafe sidecar")
					}
				}
			})
		}
	}
}

func TestPrivacyPragmasSurviveConnectionReplacement(t *testing.T) {
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	// Every query now opens a new physical connection: settings applied only to
	// the original connection must not silently disappear after reconnecting.
	s.db.SetMaxIdleConns(0)
	for range 2 {
		for pragma, want := range map[string]int{"foreign_keys": 1, "temp_store": 2, "secure_delete": 1} {
			var got int
			if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("replacement connection %s=%d, want %d: %v", pragma, got, want, err)
			}
		}
	}
}

func TestSQLiteCreatesPrivateWALFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission qualification")
	}
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.db.Exec(`INSERT INTO settings VALUES ('privacy-test','"synthetic"',0)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mailpilot.db", "mailpilot.db-wal", "mailpilot.db-shm"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("SQLite did not create private %s: %v", name, err)
		}
	}
}
