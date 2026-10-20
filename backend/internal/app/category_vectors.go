package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

// EnableCategoryVectors is composition-only, before any index operation. Category
// work shares the existing coordinator, profile, cancellation and native provider.
func (s *LocalIndex) EnableCategoryVectors(r repository.CategoryVectorRepository) {
	s.categoryVectors = r
	s.profile.CategoryRecipe = repository.CategoryRecipe
}
func (s *LocalIndex) categoryDefinition(ctx context.Context, source repository.CategoryVectorSource) ([]string, error) {
	snapshot, err := s.chunker.Build(ctx, repository.StoredEmail{Email: domain.Email{ID: domain.EmailID(source.Category.ID), AccountID: source.AccountID, Subject: source.Category.Name, BodyText: source.Category.Description, BodyState: "complete"}, ContentHash: source.DefinitionHash})
	if err != nil {
		return nil, err
	}
	inputs := []string{}
	for _, c := range snapshot.Chunks {
		inputs = append(inputs, mailtext.ModelInput(c))
	}
	if len(inputs) == 0 || len(inputs) > 20 {
		return nil, embedding.ErrInput
	}
	return inputs, nil
}
func unitMean(vectors [][]float32, dim int) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, embedding.ErrVector
	}
	sums := make([]float64, dim)
	for _, v := range vectors {
		if !validIndexVector(v, dim) {
			return nil, embedding.ErrVector
		}
		for i, x := range v {
			sums[i] += float64(x)
		}
	}
	var norm float64
	for _, x := range sums {
		norm += x * x
	}
	if norm < 1e-20 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, embedding.ErrVector
	}
	norm = math.Sqrt(norm)
	v := make([]float32, dim)
	for i, x := range sums {
		v[i] = float32(x / norm)
	}
	if !validIndexVector(v, dim) {
		return nil, embedding.ErrVector
	}
	return v, nil
}
func (s *LocalIndex) indexCategory(ctx context.Context, run repository.IndexRun, id domain.CategoryID) error {
	source, err := s.categoryVectors.Source(ctx, run.AccountID, id, run.Profile)
	if err != nil {
		return err
	}
	if !source.Category.Enabled {
		return repository.ErrConflict
	}
	if source.Cached != nil && source.Cached.SourceHash == source.SourceHash && (source.Cached.Status == "ready" || source.Cached.Status == "partial") {
		return nil
	}
	inputs, err := s.categoryDefinition(ctx, source)
	if err != nil {
		return err
	}
	var definition []float32
	if source.Cached != nil && source.Cached.DefinitionHash == source.DefinitionHash && validIndexVector(source.Cached.DefinitionVector, run.Profile.Model.Dimension) {
		definition = source.Cached.DefinitionVector
	} else {
		vectors := [][]float32{}
		for start := 0; start < len(inputs); start += embedding.MaxBatch {
			if err = ctx.Err(); err != nil {
				return err
			}
			batch := inputs[start:min(start+embedding.MaxBatch, len(inputs))]
			out, err := s.embedder.EmbedDocument(ctx, batch)
			if err != nil {
				return err
			}
			if len(out) != len(batch) {
				return embedding.ErrVector
			}
			vectors = append(vectors, out...)
		}
		definition, err = unitMean(vectors, run.Profile.Model.Dimension)
		if err != nil {
			return err
		}
	}
	components := [][]float32{definition}
	status := "ready"
	for _, e := range source.Examples {
		if e.Status != "included" {
			status = "partial"
			continue
		}
		vectors := [][]float32{}
		for _, c := range e.Chunks {
			vectors = append(vectors, c.Vector)
		}
		mean, err := unitMean(vectors, run.Profile.Model.Dimension)
		if err != nil {
			return err
		}
		components = append(components, mean)
	}
	vector, err := unitMean(components, run.Profile.Model.Dimension)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return s.categoryVectors.Save(ctx, run, source, repository.CategoryPrototype{SourceHash: source.SourceHash, DefinitionHash: source.DefinitionHash, Status: status, DefinitionInputs: inputs, DefinitionVector: definition, Vector: vector})
}

type CategoryVectorRequest struct {
	Operation  string `json:"operation"`
	AccountID  string `json:"accountId"`
	CategoryID string `json:"categoryId"`
	EmailID    string `json:"emailId"`
	Revision   string `json:"revision"`
	Approved   bool   `json:"approved"`
}

func (r CategoryVectorRequest) Valid() bool {
	if !utf8.ValidString(r.AccountID+r.CategoryID+r.EmailID) || strings.ContainsRune(r.AccountID+r.CategoryID+r.EmailID, 0) || r.AccountID == "" || len(r.AccountID) > 100 || r.CategoryID == "" || len(r.CategoryID) > 100 || len(r.EmailID) > 100 {
		return false
	}
	switch r.Operation {
	case "inspect":
		return r.EmailID == "" && r.Revision == "" && !r.Approved
	case "confirm_example":
		_, err := hex.DecodeString(r.Revision)
		return r.EmailID != "" && len(r.Revision) == 64 && strings.ToLower(r.Revision) == r.Revision && err == nil && r.Approved
	case "remove_example":
		return r.EmailID != "" && r.Revision == "" && !r.Approved
	default:
		return false
	}
}

type ExampleChunkDTO struct {
	ChunkID   string `json:"chunkId"`
	Section   string `json:"section"`
	Text      string `json:"text"`
	Context   string `json:"context"`
	Truncated bool   `json:"truncated"`
}
type CategoryExampleDTO struct {
	EmailID  string            `json:"emailId"`
	Subject  string            `json:"subject"`
	Revision string            `json:"revision"`
	Status   string            `json:"status"`
	Chunks   []ExampleChunkDTO `json:"chunks"`
}
type CategoryVectorResponse struct {
	AccountID        string               `json:"accountId"`
	CategoryID       string               `json:"categoryId"`
	Status           string               `json:"status"`
	Code             string               `json:"code"`
	SourceHash       string               `json:"sourceHash"`
	ModelID          string               `json:"modelId"`
	ModelVersion     string               `json:"modelVersion"`
	Dimension        int                  `json:"dimension"`
	Recipe           string               `json:"recipe"`
	DefinitionInputs []string             `json:"definitionInputs"`
	Examples         []CategoryExampleDTO `json:"examples"`
}

func categoryVectorError(code string) CategoryVectorResponse {
	return CategoryVectorResponse{Status: "unavailable", Code: code, DefinitionInputs: []string{}, Examples: []CategoryExampleDTO{}}
}
func (a *App) ManageCategoryVectors(ctx context.Context, r CategoryVectorRequest) CategoryVectorResponse {
	if a.Index == nil {
		return categoryVectorError("unauthorized")
	}
	return a.Index.ManageCategoryVectors(ctx, r)
}
func (s *LocalIndex) ManageCategoryVectors(ctx context.Context, r CategoryVectorRequest) CategoryVectorResponse {
	if !r.Valid() {
		return categoryVectorError("validation")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return categoryVectorError("unavailable")
	}
	a, session, err := s.session()
	if err != nil || session == nil || session.Err() != nil || string(a) != r.AccountID {
		return categoryVectorError("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	stopRoot := context.AfterFunc(s.root, cancel)
	defer stopRoot()
	if s.categoryVectors == nil {
		return categoryVectorError("unavailable")
	}
	id := domain.CategoryID(r.CategoryID)
	switch r.Operation {
	case "confirm_example":
		stored, e := s.emails.Get(ctx, a, domain.EmailID(r.EmailID))
		if e != nil {
			err = e
			break
		}
		if mailtext.ReaderRevision(stored.ContentHash) != r.Revision {
			err = repository.ErrConflict
			break
		}
		err = s.categoryVectors.Confirm(ctx, a, id, domain.EmailID(r.EmailID), stored.ContentHash)
	case "remove_example":
		err = s.categoryVectors.Remove(ctx, a, id, domain.EmailID(r.EmailID))
	}
	if err != nil {
		return categoryVectorError(categoryVectorCode(err))
	}
	source, err := s.categoryVectors.Source(ctx, a, id, s.profile)
	if err != nil {
		return categoryVectorError(categoryVectorCode(err))
	}
	inputs, err := s.categoryDefinition(ctx, source)
	if err != nil {
		return categoryVectorError(categoryVectorCode(err))
	}
	result := CategoryVectorResponse{AccountID: string(a), CategoryID: string(id), Status: "pending", ModelID: s.profile.Model.ID, ModelVersion: s.profile.Model.Version, Dimension: s.profile.Model.Dimension, Recipe: s.profile.CategoryRecipe, DefinitionInputs: inputs, Examples: []CategoryExampleDTO{}}
	if !source.Category.Enabled {
		result.Status = "disabled"
	} else if source.Cached != nil && (source.Cached.Status == "ready" || source.Cached.Status == "partial") {
		result.Status = source.Cached.Status
		result.SourceHash = source.SourceHash
	}
	for _, e := range source.Examples {
		dto := CategoryExampleDTO{EmailID: string(e.EmailID), Subject: categoryClip(e.Subject, 120), Revision: mailtext.ReaderRevision(e.ConfirmedRevision), Status: e.Status, Chunks: []ExampleChunkDTO{}}
		for _, v := range e.Chunks {
			dto.Chunks = append(dto.Chunks, ExampleChunkDTO{v.Chunk.ID, v.Chunk.SectionType, categoryClip(v.Chunk.Text, 160), v.Chunk.ContextText, len(v.Chunk.Text) > 160})
		}
		result.Examples = append(result.Examples, dto)
	}
	if session.Err() != nil {
		return categoryVectorError("unauthorized")
	}
	if ctx.Err() != nil {
		return categoryVectorError("cancelled")
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > 48*1024 {
		return categoryVectorError("too_large")
	}
	return result
}
func categoryClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func categoryVectorCode(err error) string {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return "not_found"
	case errors.Is(err, repository.ErrConflict):
		return "stale"
	case errors.Is(err, repository.ErrInvalid), errors.Is(err, embedding.ErrInput), errors.Is(err, mailtext.ErrContent):
		return "validation"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "storage_error"
	}
}
