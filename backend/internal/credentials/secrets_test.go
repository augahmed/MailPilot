package credentials

import (
	"context"
	"errors"
	"github.com/zalando/go-keyring"
	"strings"
	"testing"
)

func TestAPIKeysStayInScopedSecretStore(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	ctx := context.Background()
	a := NewAPIKeyStore("/one", "openai")
	b := NewAPIKeyStore("/two", "openai")
	c := NewAPIKeyStore("/one", "anthropic")
	if a.key == b.key || a.key == c.key {
		t.Fatal("slot collision")
	}
	if _, e := a.Load(ctx); e != ErrNotFound {
		t.Fatal(e)
	}
	if e := a.Save(ctx, NewSecret("synthetic-api-key")); e != nil {
		t.Fatal(e)
	}
	got, e := NewAPIKeyStore("/one", "openai").Load(ctx)
	if e != nil || got.Reveal() != "synthetic-api-key" {
		t.Fatal("restart failed")
	}
	for _, s := range []*APIKeyStore{b, c} {
		if _, e = s.Load(ctx); e != ErrNotFound {
			t.Fatal("cross-profile/provider read")
		}
	}
	// Separate from Gmail OAuth storage even with the same profile/provider text.
	if _, e := NewKeyring("/one", "openai").Load(ctx); e != ErrNotFound {
		t.Fatal("OAuth collision")
	}
	for _, v := range []string{"", "bad\nkey", "bad key", strings.Repeat("x", 2049)} {
		if e := a.Save(ctx, NewSecret(v)); e != ErrUnavailable {
			t.Fatal("unsafe key")
		}
	}
	if e = a.Delete(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Delete(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Load(ctx); e != ErrNotFound {
		t.Fatal("not deleted")
	}
	keyring.MockInitWithError(errors.New("private-secret-detail"))
	if _, e = a.Load(ctx); e != ErrUnavailable {
		t.Fatal(e)
	}
	if e = a.Save(ctx, NewSecret("synthetic")); e != ErrUnavailable {
		t.Fatal(e)
	}
	if e = a.Delete(ctx); e != ErrUnavailable {
		t.Fatal(e)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = a.Load(cancelCtx); e != context.Canceled {
		t.Fatal(e)
	}
	if e = a.Save(cancelCtx, NewSecret("synthetic")); e != context.Canceled {
		t.Fatal(e)
	}
	if e = a.Delete(cancelCtx); e != context.Canceled {
		t.Fatal(e)
	}
}
