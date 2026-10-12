package credentials

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// SecretStore is bound to one provider/profile slot. There is no plaintext
// fallback. API keys never pass through settings, environment or arguments.
type SecretStore interface {
	Load(context.Context) (Secret, error)
	Save(context.Context, Secret) error
	Delete(context.Context) error
}

const aiService = "com.mailpilot.desktop.ai"

type APIKeyStore struct{ key string }

// provider is trusted application configuration, never email/model output.
func NewAPIKeyStore(dataDir, provider string) *APIKeyStore {
	return &APIKeyStore{fmt.Sprintf("key-%x", sha256.Sum256([]byte(dataDir+"\x00"+provider)))}
}

func validAPIKey(s Secret) bool {
	v := s.Reveal()
	return len(v) > 0 && len(v) <= 2048 && strings.IndexFunc(v, func(r rune) bool { return r < 33 || r > 126 }) < 0
}

func (s *APIKeyStore) Load(ctx context.Context) (Secret, error) {
	if err := ctx.Err(); err != nil {
		return Secret{}, err
	}
	raw, err := keyring.Get(aiService, s.key)
	if ctx.Err() != nil {
		return Secret{}, ctx.Err()
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, ErrUnavailable
	}
	secret := NewSecret(raw)
	if !validAPIKey(secret) {
		return Secret{}, ErrUnavailable
	}
	return secret, nil
}

func (s *APIKeyStore) Save(ctx context.Context, secret Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validAPIKey(secret) {
		return ErrUnavailable
	}
	if keyring.Set(aiService, s.key, secret.Reveal()) != nil {
		return ErrUnavailable
	}
	return ctx.Err()
}

func (s *APIKeyStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := keyring.Delete(aiService, s.key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return ErrUnavailable
	}
	return ctx.Err()
}

var _ SecretStore = (*APIKeyStore)(nil)
