package embedding

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
)

const MaxBatchSize = 32
const MaxDimension = 8192

var (
	ErrInput       = errors.New("invalid embedding input")
	ErrUnavailable = errors.New("embedding unavailable")
	ErrProvider    = errors.New("embedding provider failed")
	ErrVector      = errors.New("invalid embedding vector")
)

// EmbeddingProvider implementations must honor cancellation and return one
// finite, nonzero vector per input, preserving batch order. ProviderName is an
// immutable identity including model revision and settings affecting vectors;
// changing weights/settings requires a new identity even at the same dimension.
// This phase supports trusted on-device adapters only. External adapters require
// a separate exact-payload consent boundary before they can be wired here.
type EmbeddingProvider interface {
	EmbedText(context.Context, string) ([]float32, error)
	EmbedBatch(context.Context, []string) ([][]float32, error)
	Dimension() int
	ProviderName() string
}

type Key struct {
	AccountID domain.AccountID
	EmailID   domain.EmailID
	Provider  string
	Dimension int
	Version   string
	InputHash string
}

type Record struct {
	Key        Key
	SourceHash string // Optimistic guard against a changed/deleted source snapshot.
	Vector     []float32
}

// VectorStore is a local cache, not a similarity index. PutBatch is atomic and
// rejects stale source snapshots. Missing entries use repository.ErrNotFound.
type VectorStore interface {
	Get(context.Context, Key) ([]float32, error)
	PutBatch(context.Context, []Record) error
}

func (k Key) Validate() error {
	if k.AccountID == "" || k.EmailID == "" || !validProvider(k.Provider, k.Dimension) || k.Version != PreprocessingVersion || len(k.InputHash) != 64 {
		return ErrInput
	}
	if !validHash(k.InputHash) {
		return ErrInput
	}
	return nil
}

func validHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func validProvider(name string, dimension int) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 256 && utf8.ValidString(name) &&
		strings.IndexFunc(name, unicode.IsControl) < 0 && dimension >= 1 && dimension <= MaxDimension
}

func ValidateVector(v []float32, dimension int) error {
	if dimension < 1 || dimension > MaxDimension || len(v) != dimension {
		return ErrVector
	}
	nonzero := false
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return ErrVector
		}
		nonzero = nonzero || f != 0
	}
	if !nonzero {
		return ErrVector
	}
	return nil
}
