package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zalando/go-keyring"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretsRedactedInFormattingJSONAndLogs(t *testing.T) {
	secret := NewSecret("synthetic-token-canary")
	record := Record{RefreshToken: secret, AccountID: "id", Email: "reader@example.test"}
	for _, value := range []any{secret, &secret, record, &record, struct{ Token Secret }{secret}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(format, value), secret.Reveal()) {
				t.Fatalf("unsafe format %s", format)
			}
		}
		raw, _ := json.Marshal(value)
		if bytes.Contains(raw, []byte(secret.Reveal())) {
			t.Fatal("secret in JSON")
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("test", "value", value)
		if strings.Contains(logs.String(), secret.Reveal()) {
			t.Fatal("secret in logs")
		}
	}
}
func TestKeyringAdapterRoundTripAndErrors(t *testing.T) {
	keyring.MockInit() // no real OS store touched by unit tests
	store := NewKeyring(t.TempDir(), "test-client")
	ctx := context.Background()
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected missing")
	}
	record := Record{NewSecret("synthetic-refresh"), "id", "reader@example.test"}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx)
	if err != nil || got.RefreshToken.Reveal() != record.RefreshToken.Reveal() || got.Email != record.Email {
		t.Fatal("credential round trip failed")
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("synthetic-private-token-error"))
	if err := store.Save(ctx, record); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unsafe save error")
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unsafe load error")
	}
	if err := store.Delete(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unsafe delete error")
	}
	keyring.MockInit()
}
func TestKeyringScopeAndCorruption(t *testing.T) {
	keyring.MockInit()
	ctx := context.Background()
	a := NewKeyring("/one", "a")
	b := NewKeyring("/two", "a")
	c := NewKeyring("/one", "b")
	if a.Reference() == b.Reference() || a.Reference() == c.Reference() {
		t.Fatal("credential namespaces collide")
	}
	_ = keyring.Set(service, a.key, `{"version":1,"refresh":"synthetic"}`)
	if _, err := a.Load(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt credential accepted")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
