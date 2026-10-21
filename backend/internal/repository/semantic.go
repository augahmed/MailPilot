package repository

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

const MaxSemanticCategories = 100

type SemanticPrototype struct {
	Category           domain.EmailCategory
	SourceHash, Status string
	Vector             []float32
}
type SemanticSnapshot struct {
	Email                                               StoredEmail
	Categories                                          []domain.EmailCategory
	Profile                                             IndexProfile
	Revision, EmailRevision, BaseHash, Hash, Generation string
	Availability                                        string
	Chunks                                              []VectorSource
	Prototypes                                          []SemanticPrototype
	Preference                                          domain.CategoryPreference
}

func SemanticProfile(p IndexProfile) domain.SemanticProfile {
	return domain.SemanticProfile{ModelID: p.Model.ID, ModelVersion: p.Model.Version, Dimension: p.Model.Dimension, Fingerprint: p.Fingerprint, PreprocessingVersion: p.PreprocessingVersion, IndexVersion: p.IndexVersion, CategoryRecipe: p.CategoryRecipe}
}
func IndexProfileFromSemantic(p domain.SemanticProfile) IndexProfile {
	return IndexProfile{Model: EmbeddingModel{ID: p.ModelID, Version: p.ModelVersion, Dimension: p.Dimension}, Fingerprint: p.Fingerprint, PreprocessingVersion: p.PreprocessingVersion, IndexVersion: p.IndexVersion, CategoryRecipe: p.CategoryRecipe}
}

// SemanticRepository reads existing vectors only. It never embeds mail, owns a
// worker, or grants Gmail write authority. Background work shares IndexRun fences.
type SemanticProgress struct{ Eligible, Assessed, Pending int }
type SemanticRepository interface {
	Prepare(context.Context, IndexRun, string) (IndexRun, error)
	Progress(context.Context, domain.AccountID, string) (SemanticProgress, error)
	Snapshot(context.Context, domain.AccountID, domain.EmailID, IndexProfile) (SemanticSnapshot, error)
	Next(context.Context, IndexRun, string) ([]domain.EmailID, error)
	SaveIndexed(context.Context, IndexRun, domain.ClassificationRun) error
	SetPreference(context.Context, domain.AccountID, domain.EmailID, string, domain.CategoryID, string) error
}
