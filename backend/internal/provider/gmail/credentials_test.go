package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

type tokenSourceFunc func(context.Context, domain.AccountID) (credentials.Secret, error)

func (f tokenSourceFunc) AccessToken(ctx context.Context, account domain.AccountID) (credentials.Secret, error) {
	return f(ctx, account)
}

func TestCredentialFailuresNeverDispatchMailboxRequests(t *testing.T) {
	operations := []struct {
		name string
		call func(context.Context, *Provider) error
	}{
		{"authenticate", func(ctx context.Context, p *Provider) error { _, err := p.Authenticate(ctx); return err }},
		{"read", func(ctx context.Context, p *Provider) error {
			_, err := p.ListMessages(ctx, provider.ListMessagesOptions{Page: provider.PageOptions{Limit: 10}})
			return err
		}},
		{"create label", func(ctx context.Context, p *Provider) error {
			_, err := p.CreateLabel(ctx, "MailPilot/Receipts")
			return err
		}},
	}
	for _, operation := range operations {
		for _, cause := range []string{"empty_token", "token_source_error", "cancelled_during_token_access"} {
			t.Run(operation.name+"/"+cause, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
				defer server.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				tokens := tokenSourceFunc(func(context.Context, domain.AccountID) (credentials.Secret, error) {
					switch cause {
					case "token_source_error":
						return credentials.Secret{}, errors.New("synthetic credential detail")
					case "cancelled_during_token_access":
						cancel()
						return credentials.Secret{}, ctx.Err()
					default:
						return credentials.Secret{}, nil
					}
				})
				p := NewProvider("a", tokens)
				p.baseURL = server.URL
				p.client = server.Client()
				expected := provider.ErrAuthenticationRequired
				if cause == "cancelled_during_token_access" {
					expected = context.Canceled
				}
				if err := operation.call(ctx, p); !errors.Is(err, expected) {
					t.Fatal("incorrect sanitized credential error")
				}
				if requests.Load() != 0 {
					t.Fatal("credential failure dispatched a mailbox request")
				}
			})
		}
	}
}
