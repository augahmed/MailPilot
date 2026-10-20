package repository

import (
	"context"
	"mailpilot.local/core/internal/domain"
)

const CategoryRecipe = "category-prototype-v1-definition-and-examples-equal-unit-mean"
const MaxCategoryExamples = 10
const MaxExampleChunks = 3

type CategoryExample struct {
	EmailID                                                                         domain.EmailID
	ConfirmationID, ConfirmedRevision, CurrentRevision, Status, Generation, Subject string
	Chunks                                                                          []VectorSource
}
type CategoryPrototype struct {
	SourceHash, DefinitionHash, Status string
	DefinitionInputs                   []string
	DefinitionVector, Vector           []float32
}
type CategoryVectorSource struct {
	AccountID                            domain.AccountID
	Category                             domain.EmailCategory
	Revision, SourceHash, DefinitionHash string
	Profile                              IndexProfile
	Examples                             []CategoryExample
	Cached                               *CategoryPrototype
}
type CategoryVectorProgress struct{ Eligible, Ready, Partial, Pending int }

// Confirm only records an explicit current-source selection. Assignments and
// predictions are never read as examples. All vector publication uses IndexRun.
type CategoryVectorRepository interface {
	Confirm(context.Context, domain.AccountID, domain.CategoryID, domain.EmailID, string) error
	Remove(context.Context, domain.AccountID, domain.CategoryID, domain.EmailID) error
	Source(context.Context, domain.AccountID, domain.CategoryID, IndexProfile) (CategoryVectorSource, error)
	Save(context.Context, IndexRun, CategoryVectorSource, CategoryPrototype) error
	Next(context.Context, IndexRun) ([]domain.CategoryID, error)
	Progress(context.Context, domain.AccountID, IndexProfile) (CategoryVectorProgress, error)
}
