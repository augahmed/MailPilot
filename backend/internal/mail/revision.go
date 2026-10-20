package mail

import (
	"crypto/sha256"
	"fmt"
)

// ReaderRevision binds navigation to the same source and cleaned projection.
func ReaderRevision(source string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(TextProjectionVersion+"\n"+source)))
}
