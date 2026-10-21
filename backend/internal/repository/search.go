package repository

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

// SearchFilter applies to original metadata and assigned local categories. Dates
// are UTC epoch milliseconds, inclusive After and exclusive Before; zero omits.
type SearchFilter struct {
	Sender     string `json:"sender"`
	After      int64  `json:"after"`
	Before     int64  `json:"before"`
	CategoryID string `json:"categoryId"`
}
type VectorSource struct {
	Chunk                              EmailChunk
	Generation                         string
	Subject, SenderName, SenderAddress string
	ReceivedAt                         int64
	Vector                             []float32
	Keyword                            bool
	Partial                            bool
}

// KeywordQuery contains escaped FTS terms and literal identifiers built by Go.
// No caller-supplied MATCH syntax or SQL crosses IPC.
type KeywordQuery struct {
	Match    string
	Literals []string
}
type KeywordRepository interface {
	ScanKeywords(context.Context, domain.AccountID, IndexProfile, SearchFilter, KeywordQuery, func(VectorSource, float64) error) error
	CheckKeywordSources(context.Context, domain.AccountID, IndexProfile, SearchFilter, []VectorSource) error
	KeywordCoverage(context.Context, domain.AccountID, IndexProfile) (int, error)
}

// VectorRepository streams a consistent SQLite snapshot. Callbacks must not call
// the store (one DB connection). Vector slices are valid only during the callback;
// copy before retaining them. No inference runs inside this read transaction.
type VectorRepository interface {
	ScanVectors(context.Context, domain.AccountID, IndexProfile, SearchFilter, func(VectorSource) error) error
	CheckVectorSources(context.Context, domain.AccountID, IndexProfile, SearchFilter, []VectorSource) error
}
