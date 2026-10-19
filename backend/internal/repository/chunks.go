package repository

import (
	"context"
	"time"

	"mailpilot.local/core/internal/domain"
)

// EmbeddingModel identifies the complete embedding recipe, including tokenizer,
// pooling and query/document input policy. Changing that recipe requires a new Version.
type EmbeddingModel struct {
	ID        string
	Version   string
	Dimension int
}

// ChunkInput uses half-open UTF-8 byte offsets into its named source projection
// (a caller-defined projection for legacy rows without SourceKind).
// Index must be contiguous from zero. Hash is SHA-256 of Text; empty computes it.
// SectionType names a normalized section (e.g. subject, body); it grants no trust.
type ChunkInput struct {
	Index                  int
	SectionType, Text      string
	TokenCount             int
	StartOffset, EndOffset int
	ContentHash            string
	// SourceKind identifies the deterministic subject/plain/HTML projection.
	// Legacy rows have no mapping. Offsets are bytes within that projection.
	SourceKind                           string
	SectionIndex                         int
	SectionStartOffset, SectionEndOffset int
	// ContextText is the bounded model-input prefix; Text stays an exact passage.
	ContextText string
}

type EmailChunk struct {
	ChunkInput
	ID                   string
	AccountID            domain.AccountID
	EmailID              domain.EmailID
	SourceRevision       string
	PreprocessingVersion string
}

// ChunkSnapshot replaces the whole chunk set atomically. SourceRevision is the
// cached email's EmailContentHash, not the provider's opaque ContentRevision.
type ChunkSnapshot struct {
	AccountID                            domain.AccountID
	EmailID                              domain.EmailID
	SourceRevision, PreprocessingVersion string
	IndexVersion                         string
	Model                                EmbeddingModel
	Chunks                               []ChunkInput
	ContentStatus                        string // complete, partial, unavailable; empty derives cached coverage.
}

const (
	IndexPending     = "pending"
	IndexPartial     = "partial"
	IndexReady       = "ready"
	IndexUnavailable = "unavailable"
	IndexStale       = "stale"
	EmbeddingPending = "pending"
	EmbeddingReady   = "ready"
	EmbeddingFailed  = "failed"
)

type IndexState struct {
	AccountID                            domain.AccountID
	EmailID                              domain.EmailID
	SourceRevision, PreprocessingVersion string
	IndexVersion, Generation             string
	Model                                EmbeddingModel
	Status, BodyState                    string
	ContentStatus                        string
	LastIndexedAt                        *time.Time
}

type ChunkEmbedding struct {
	ChunkID string
	Status  string
	Vector  []float32
}

// EmbeddingBatch is bound to a snapshot returned by Replace/State. Its generation
// rejects results computed for superseded snapshots, including configuration ABA.
type EmbeddingBatch struct {
	State      IndexState
	Embeddings []ChunkEmbedding
}

// ChunkRepository is storage only: no inference, scheduling, retrieval or fallback
// to historical per-email vectors. All reads are scoped to live current snapshots.
type ChunkRepository interface {
	Replace(context.Context, ChunkSnapshot) (IndexState, error)
	State(context.Context, domain.AccountID, domain.EmailID) (IndexState, error)
	List(context.Context, domain.AccountID, domain.EmailID, Page) ([]EmailChunk, error)
	SaveEmbeddings(context.Context, EmbeddingBatch) (IndexState, error)
	Embedding(context.Context, domain.AccountID, domain.EmailID, string, EmbeddingModel) (ChunkEmbedding, error)
}
