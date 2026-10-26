//go:build !darwin && !linux

package sqlite

import "os"

// Unix permission/link checks are qualified on macOS and Linux. Other platforms
// still reject symlinks and nonregular files; Windows ACL qualification is separate.
func hasMultipleLinks(os.FileInfo) bool { return false }
