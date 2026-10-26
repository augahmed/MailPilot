package sqlite

import (
	"errors"
	"os"
)

// SQLite may replay an existing WAL/rollback journal before running any query.
// Check all of its fixed files before opening the database, and repair permissions
// left by older versions or restored copies. Newly created SQLite sidecars inherit
// the main database's private mode on the supported Unix platforms.
func prepareDatabaseFiles(dataDir string) error {
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	names := []string{"mailpilot.db", "mailpilot.db-wal", "mailpilot.db-shm", "mailpilot.db-journal"}
	infos := make([]os.FileInfo, len(names))
	for i, name := range names {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || hasMultipleLinks(info) {
			return errors.New("database files must be regular files without links")
		}
		infos[i] = info
	}
	for i, name := range names {
		flags := os.O_RDWR
		if infos[i] == nil {
			if i != 0 {
				continue
			}
			flags |= os.O_CREATE | os.O_EXCL
		}
		file, err := root.OpenFile(name, flags, 0600)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err == nil && (!info.Mode().IsRegular() || hasMultipleLinks(info) || (infos[i] != nil && !os.SameFile(infos[i], info))) {
			err = errors.New("database file changed during initialization")
		}
		if err == nil {
			err = file.Chmod(0600)
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
