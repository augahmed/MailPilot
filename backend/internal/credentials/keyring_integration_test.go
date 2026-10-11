//go:build keyring_integration

package credentials

import (
	"context"
	"testing"
)

// Explicit opt-in: creates one synthetic credential in a unique temporary-profile
// namespace and removes it. Run this test alone, never alongside MockInit tests.
func TestNativeKeyring(t *testing.T) {
	store := NewKeyring(t.TempDir(), "synthetic-integration-client")
	ctx := context.Background()
	t.Cleanup(func() {
		if err := store.Delete(ctx); err != nil {
			t.Error("synthetic keyring cleanup failed")
		}
	})
	record := Record{RefreshToken: NewSecret("synthetic-not-a-real-oauth-token"), AccountID: "synthetic-account", Email: "reader@example.test"}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal("native credential write failed")
	}
	actual, err := store.Load(ctx)
	if err != nil || actual.RefreshToken.Reveal() != record.RefreshToken.Reveal() {
		t.Fatal("native credential read failed")
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal("native credential deletion failed")
	}
}
