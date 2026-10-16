package sqlite

import (
	"mailpilot.local/core/internal/domain"
	"time"
)

func legacyEmail(a domain.AccountID, id, subject, body, sender string, day int) domain.Email {
	return domain.Email{ID: domain.EmailID(id), AccountID: a, ProviderID: domain.ProviderMessageID(id), Subject: subject, BodyText: body, BodyState: "complete", Sender: domain.EmailAddress{Address: sender}, ReceivedAt: time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC)}
}
