package provider_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

func TestPaginationValidation(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 100, 101} {
		options := provider.ListMessagesOptions{Page: provider.PageOptions{Limit: limit, PageToken: "opaque+/=:token"}}
		err := options.Validate()
		if limit >= 1 && limit <= 100 {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, provider.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	zero := time.Time{}
	for _, options := range []provider.ListMessagesOptions{
		{Page: provider.PageOptions{Limit: 10}, ReceivedAfter: &zero},
		{Page: provider.PageOptions{Limit: 10}, LabelIDs: []domain.ProviderLabelID{""}},
	} {
		if !errors.Is(options.Validate(), provider.ErrInvalidArgument) {
			t.Fatal("invalid filters accepted")
		}
	}
}

func TestDraftValidationRejectsHeaderInjection(t *testing.T) {
	address := domain.EmailAddress{Name: "Example Person", Address: "person@example.test"}
	for name, draft := range map[string]provider.DraftInput{
		"empty draft":                     {},
		"body line breaks":                {Recipients: []domain.EmailAddress{address}, Subject: "Hello", BodyText: "first\r\nsecond"},
		"subject injection":               {Subject: "Hello\r\nBcc: other@example.test"},
		"to injection":                    {Recipients: []domain.EmailAddress{{Address: "person@example.test\r\nBcc: other@example.test"}}},
		"cc name injection":               {CCRecipients: []domain.EmailAddress{{Name: "name\nBcc: other", Address: address.Address}}},
		"bcc invalid mailbox":             {BCCRecipients: []domain.EmailAddress{{Address: "not a mailbox"}}},
		"multiple addresses in one field": {Recipients: []domain.EmailAddress{{Address: "a@example.test,b@example.test"}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := draft.Validate()
			valid := name == "empty draft" || name == "body line breaks"
			if valid && err != nil {
				t.Fatal(err)
			}
			if !valid && !errors.Is(err, provider.ErrInvalidArgument) {
				t.Fatalf("expected safe invalid-argument error, got %v", err)
			}
		})
	}
}

// This synthetic adapter demonstrates the interface contract without importing a
// provider SDK. These tests do not claim to validate any real provider behavior.
type readOnlyProvider struct{}

var _ provider.EmailProvider = readOnlyProvider{}

func (readOnlyProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (readOnlyProvider) Authenticate(ctx context.Context) (provider.Account, error) {
	if err := ctx.Err(); err != nil {
		return provider.Account{}, err
	}
	return provider.Account{ID: "account-a", Provider: "fixture", ProviderAccountID: "remote-account", Address: domain.EmailAddress{Address: "person@example.test"}}, nil
}
func (readOnlyProvider) ListMessages(ctx context.Context, o provider.ListMessagesOptions) (provider.MessagePage, error) {
	if err := ctx.Err(); err != nil {
		return provider.MessagePage{}, err
	}
	if err := o.Validate(); err != nil {
		return provider.MessagePage{}, err
	}
	return provider.MessagePage{Messages: []provider.MessageReference{{AccountID: "account-a", ProviderID: "remote-message"}}}, nil
}
func (readOnlyProvider) FetchMessage(ctx context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	if err := ctx.Err(); err != nil {
		return domain.Email{}, err
	}
	if id != "remote-message" {
		return domain.Email{}, fmt.Errorf("fetch: %w", provider.ErrNotFound)
	}
	return domain.Email{AccountID: "account-a", ProviderID: id, ReceivedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}, nil
}
func (readOnlyProvider) FetchThread(context.Context, domain.ProviderThreadID, provider.PageOptions) (domain.EmailThread, error) {
	return domain.EmailThread{}, provider.ErrUnsupported
}
func (readOnlyProvider) ListLabels(context.Context, provider.PageOptions) (provider.LabelPage, error) {
	return provider.LabelPage{}, nil
}
func (readOnlyProvider) CreateLabel(context.Context, string) (domain.EmailLabel, error) {
	return domain.EmailLabel{}, provider.ErrUnsupported
}
func (readOnlyProvider) ApplyLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error {
	return provider.ErrUnsupported
}
func (readOnlyProvider) RemoveLabel(context.Context, domain.ProviderMessageID, domain.ProviderLabelID) error {
	return provider.ErrUnsupported
}
func TestProviderCanBeConsumedWithoutAnSDK(t *testing.T) {
	var adapter provider.EmailProvider = readOnlyProvider{}
	var reader provider.Reader = adapter
	ctx := context.Background()
	account, err := adapter.Authenticate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader.ListMessages(ctx, provider.ListMessagesOptions{Page: provider.PageOptions{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	email, err := reader.FetchMessage(ctx, page.Messages[0].ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if email.AccountID != account.ID {
		t.Fatal("wrong account")
	}
	if err := email.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.FetchMessage(ctx, "absent"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatal("not-found error lost")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reader.FetchMessage(cancelled, "remote-message"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	if adapter.Capabilities().CreateDrafts {
		t.Fatal("unsupported draft capability advertised")
	}
}

func TestProviderDoesNotExposeSendingOrDeletion(t *testing.T) {
	contract := reflect.TypeFor[provider.EmailProvider]()
	for _, name := range []string{"CreateDraft", "Send", "SendMail", "SendDraft", "Delete", "DeleteMessage", "MoveMessage"} {
		if _, exists := contract.MethodByName(name); exists {
			t.Fatalf("unsafe operation exposed: %s", name)
		}
	}
}
