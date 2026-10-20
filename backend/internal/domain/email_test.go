package domain_test

import (
	"reflect"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
)

func message() domain.Email {
	return domain.Email{AccountID: "account-a", ProviderID: "opaque/id:9007199254740993", ProviderThreadID: "thread-a", ReceivedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

func TestHeadersPreserveDuplicatesAndOrder(t *testing.T) {
	email := message()
	email.Headers = []domain.EmailHeader{{Name: "Received", Value: "first"}, {Name: "Subject", Value: "other"}, {Name: "received", Value: "second"}}
	got := email.HeaderValues("RECEIVED")
	if !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("unexpected headers: %v", got)
	}
	got[0] = "changed"
	if email.Headers[0].Value != "first" {
		t.Fatal("header lookup aliases stored headers")
	}
	if len(email.HeaderValues("Missing")) != 0 {
		t.Fatal("unexpected missing header")
	}
}

func TestIncomingEmailPreservesIncompleteAndHostileData(t *testing.T) {
	email := message()
	email.Sender = domain.EmailAddress{Name: "Untrusted sender", Address: "malformed mailbox"}
	// Remote results have no local ID yet. Empty bodies, subjects and recipients
	// are representable and must not cause suspicious email to be discarded.
	if err := email.Validate(); err != nil {
		t.Fatal(err)
	}
	emptyHTML := ""
	email.BodyHTML = &emptyHTML
	zero := int64(0)
	email.Attachments = []domain.AttachmentMetadata{{Filename: "../untrusted.txt", SizeBytes: &zero}, {Filename: "unknown-size"}}
	if err := email.Validate(); err != nil {
		t.Fatal(err)
	}
	if email.BodyHTML == nil || email.Attachments[1].SizeBytes != nil {
		t.Fatal("optional value distinctions lost")
	}
}

func TestEmailStructuralValidation(t *testing.T) {
	negative := int64(-1)
	for name, change := range map[string]func(*domain.Email){
		"missing account":      func(e *domain.Email) { e.AccountID = "" },
		"missing provider id":  func(e *domain.Email) { e.ProviderID = "" },
		"missing receive time": func(e *domain.Email) { e.ReceivedAt = time.Time{} },
		"cross-account label": func(e *domain.Email) {
			e.Labels = []domain.EmailLabel{{AccountID: "other", ProviderID: "label", Kind: domain.LabelKindUser}}
		},
		"missing label id": func(e *domain.Email) {
			e.Labels = []domain.EmailLabel{{AccountID: e.AccountID, Kind: domain.LabelKindUser}}
		},
		"unknown label kind": func(e *domain.Email) {
			e.Labels = []domain.EmailLabel{{AccountID: e.AccountID, ProviderID: "label", Kind: "unknown"}}
		},
		"negative attachment size": func(e *domain.Email) { e.Attachments = []domain.AttachmentMetadata{{SizeBytes: &negative}} },
	} {
		t.Run(name, func(t *testing.T) {
			email := message()
			change(&email)
			if email.Validate() == nil {
				t.Fatal("invalid structure accepted")
			}
		})
	}
	email := message()
	email.Labels = []domain.EmailLabel{{AccountID: email.AccountID, ProviderID: "label", Kind: domain.LabelKindSystem}}
	email.Categories = []domain.EmailCategory{{ID: "follow-up", Name: "Follow up"}}
	if err := email.Validate(); err != nil {
		t.Fatal(err)
	}
}
