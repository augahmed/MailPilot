package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mailpilot.local/core/internal/domain"
	"time"
)

// EmailContentHash binds cached and live snapshots to the same normalized inputs.
// Recipe v2 excludes attachment retrieval handles as well as message identities.
// Old hashes remain readable; a normal source upsert invalidates derived state
// when moving an affected source to this recipe. Immutable plans are not rewritten.
func EmailContentHash(e domain.Email) (string, error) {
	// IDs, provider label membership and local categories are not analysis inputs.
	e.ID = ""
	e.AccountID = ""
	e.ProviderID = ""
	e.ProviderThreadID = ""
	e.ContentRevision = ""
	e.Labels = nil
	e.Categories = nil
	e.ReceivedAt = time.UnixMilli(e.ReceivedAt.UnixMilli()).UTC()
	e.Recipients = append([]domain.EmailAddress{}, e.Recipients...)
	e.CCRecipients = append([]domain.EmailAddress{}, e.CCRecipients...)
	e.Headers = append([]domain.EmailHeader{}, e.Headers...)
	e.Attachments = append([]domain.AttachmentMetadata{}, e.Attachments...)
	for i := range e.Attachments {
		// ProviderID is a download handle, not attachment content. Preserve it
		// in the original cached metadata, but retain only stable descriptors
		// (part, filename, MIME type, size, disposition and Content-ID) here.
		e.Attachments[i].ProviderID = ""
	}
	data, err := json.Marshal(e)
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
