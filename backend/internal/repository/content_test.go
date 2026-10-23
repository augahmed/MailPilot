package repository

import (
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
)

func TestContentHashExcludesAttachmentRetrievalHandles(t *testing.T) {
	size := int64(128)
	original := domain.Email{Subject: "Receipt", BodyText: "Original source", ReceivedAt: time.UnixMilli(1000), Attachments: []domain.AttachmentMetadata{{ProviderID: "download-1", PartID: "1.2", Filename: "image.png", MIMEType: "image/png", SizeBytes: &size, Inline: true, ContentID: "<image>"}}}
	hash := func(e domain.Email) string {
		t.Helper()
		h, err := EmailContentHash(e)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	before := hash(original)
	changed := original
	changed.Attachments = append([]domain.AttachmentMetadata{}, original.Attachments...)
	changed.Attachments[0].ProviderID = "download-2"
	if hash(changed) != before {
		t.Fatal("retrieval handle invalidated source identity")
	}
	if original.Attachments[0].ProviderID != "download-1" || changed.Attachments[0].ProviderID != "download-2" {
		t.Fatal("hashing mutated original metadata")
	}
	for name, mutate := range map[string]func(*domain.Email){
		"part":         func(e *domain.Email) { e.Attachments[0].PartID = "1.3" },
		"filename":     func(e *domain.Email) { e.Attachments[0].Filename = "other.png" },
		"mime":         func(e *domain.Email) { e.Attachments[0].MIMEType = "application/pdf" },
		"size":         func(e *domain.Email) { n := int64(129); e.Attachments[0].SizeBytes = &n },
		"unknown_size": func(e *domain.Email) { e.Attachments[0].SizeBytes = nil },
		"inline":       func(e *domain.Email) { e.Attachments[0].Inline = false },
		"content_id":   func(e *domain.Email) { e.Attachments[0].ContentID = "<other>" },
		"removed":      func(e *domain.Email) { e.Attachments = nil },
		"body":         func(e *domain.Email) { e.BodyText = "Changed source" },
		"header":       func(e *domain.Email) { e.Headers = []domain.EmailHeader{{Name: "From", Value: "other@example.test"}} },
	} {
		t.Run(name, func(t *testing.T) {
			e := original
			e.Attachments = append([]domain.AttachmentMetadata{}, original.Attachments...)
			mutate(&e)
			if hash(e) == before {
				t.Fatal("meaningful source change did not invalidate identity")
			}
		})
	}
}
