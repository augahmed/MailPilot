package repository

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

// IndexProfile identifies the only compatible chunk/embedding generation.
type IndexProfile struct {
	Model                                           EmbeddingModel
	CategoryRecipe                                  string
	PreprocessingVersion, IndexVersion, Fingerprint string
}
type IndexRun struct {
	AccountID                domain.AccountID
	Profile                  IndexProfile
	Generation, Status, Code string
}
type IndexProgress struct {
	Eligible, Indexed, Complete, Partial, Failed, Pending, Unavailable int
	Chunks, Embeddings                                                 int
}
type IndexCandidate struct {
	EmailID        domain.EmailID
	SourceRevision string
}

// IndexRepository extends the shared store with one durable account job. The run
// generation fences every background publication, including chunk replacement.
type IndexRepository interface {
	Recover(context.Context) error
	Begin(context.Context, domain.AccountID, IndexProfile, bool) (IndexRun, error)
	Run(context.Context, domain.AccountID) (IndexRun, error)
	Transition(context.Context, IndexRun, string, string) error
	Next(context.Context, IndexRun) ([]IndexCandidate, error)
	Progress(context.Context, domain.AccountID, IndexProfile) (IndexProgress, error)
	ReplaceIndexed(context.Context, IndexRun, ChunkSnapshot) (IndexState, error)
	SaveIndexed(context.Context, IndexRun, EmbeddingBatch) (IndexState, error)
	Fail(context.Context, IndexRun, IndexCandidate, string) error
}
