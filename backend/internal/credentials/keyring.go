package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const service = "com.mailpilot.desktop.oauth"

type Keyring struct{ key string }

// A different data directory or OAuth client has a different credential slot.
func NewKeyring(dataDir, clientID string) *Keyring {
	return &Keyring{key: fmt.Sprintf("gmail-%x", sha256.Sum256([]byte(dataDir+"\x00"+clientID)))}
}
func (k *Keyring) Reference() string { return service + ":" + k.key }

// Deliberately private: the only plaintext JSON representation is inside the OS store.
type storedRecord struct {
	Version   int    `json:"version"`
	Refresh   string `json:"refresh"`
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
}

func (k *Keyring) Load(ctx context.Context) (Record, error) {
	if ctx.Err() != nil {
		return Record{}, ctx.Err()
	}
	raw, err := keyring.Get(service, k.key)
	if errors.Is(err, keyring.ErrNotFound) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, ErrUnavailable
	}
	if ctx.Err() != nil {
		return Record{}, ctx.Err()
	}
	var data storedRecord
	if len(raw) > 2200 || json.Unmarshal([]byte(raw), &data) != nil || data.Version != 1 || data.Refresh == "" || data.Email == "" || data.AccountID == "" {
		return Record{}, ErrUnavailable
	}
	return Record{RefreshToken: NewSecret(data.Refresh), AccountID: data.AccountID, Email: data.Email}, nil
}
func (k *Keyring) Save(ctx context.Context, record Record) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if record.RefreshToken.Empty() || record.Email == "" || record.AccountID == "" {
		return ErrUnavailable
	}
	raw, err := json.Marshal(storedRecord{1, record.RefreshToken.Reveal(), record.AccountID, record.Email})
	// Fits macOS security's interactive line and Windows credential limits.
	if err != nil || len(raw) > 2200 {
		return ErrUnavailable
	}
	if keyring.Set(service, k.key, string(raw)) != nil {
		return ErrUnavailable
	}
	return nil
}
func (k *Keyring) Delete(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := keyring.Delete(service, k.key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return ErrUnavailable
	}
	return nil
}
