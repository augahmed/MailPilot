package app

import (
	"context"
	"encoding/hex"
	"strings"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

type ReviewCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SemanticReviewEvidence struct {
	ChunkID    string  `json:"chunkId"`
	Section    string  `json:"section"`
	Text       string  `json:"text"`
	Similarity float64 `json:"similarity"`
	Weight     float64 `json:"weight"`
}
type SemanticReviewScore struct {
	CategoryID      string                   `json:"categoryId"`
	CategoryName    string                   `json:"categoryName"`
	Score           float64                  `json:"score"`
	PrototypeHash   string                   `json:"prototypeHash"`
	PrototypeStatus string                   `json:"prototypeStatus"`
	ChunkCount      int                      `json:"chunkCount"`
	Evidence        []SemanticReviewEvidence `json:"evidence"`
}
type SemanticReviewDTO struct {
	State             string                `json:"state"`
	ModelVersion      string                `json:"modelVersion"`
	ClassifierVersion string                `json:"classifierVersion"`
	SourceRevision    string                `json:"sourceRevision"`
	MinimumScore      float64               `json:"minimumScore"`
	MinimumMargin     float64               `json:"minimumMargin"`
	Assigned          []ReviewCategory      `json:"assigned"`
	Scores            []SemanticReviewScore `json:"scores"`
}

func validReviewRevision(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil && strings.ToLower(s) == s
}
func (s *ClassificationReview) EnableSemantic(repo repository.SemanticRepository, chunks repository.ChunkRepository) {
	s.semantic = repo
	s.chunks = chunks
}
func (s *ClassificationReview) preference(ctx context.Context, account domain.AccountID, r ClassificationRequest) error {
	getter, ok := s.emails.(interface {
		Get(context.Context, domain.AccountID, domain.EmailID) (repository.StoredEmail, error)
	})
	if !ok || s.semantic == nil {
		return repository.ErrInvalid
	}
	stored, err := getter.Get(ctx, account, domain.EmailID(r.EmailID))
	if err != nil {
		return err
	}
	if mailtext.ReaderRevision(stored.ContentHash) != r.Revision {
		return repository.ErrConflict
	}
	return s.semantic.SetPreference(ctx, account, stored.Email.ID, r.Operation, domain.CategoryID(r.CategoryID), stored.ContentHash)
}
func (s *ClassificationReview) semanticDTO(ctx context.Context, run domain.ClassificationRun, names map[domain.CategoryID]string) (*SemanticReviewDTO, error) {
	sem := run.Semantic
	if sem == nil {
		return nil, nil
	}
	dto := &SemanticReviewDTO{State: sem.State, ModelVersion: sem.Profile.ModelVersion, ClassifierVersion: run.ClassifierVersion, SourceRevision: sem.SourceRevision, MinimumScore: sem.MinimumScore, MinimumMargin: sem.MinimumMargin, Assigned: []ReviewCategory{}, Scores: []SemanticReviewScore{}}
	for _, id := range sem.Assigned {
		dto.Assigned = append(dto.Assigned, ReviewCategory{string(id), shortText(names[id], 100)})
	}
	texts := map[string]string{}
	// Shared passages are read only for the bounded visible evidence, not copied
	// into another persistent email-text cache.
	if len(sem.Scores) > 0 && s.chunks != nil {
		for offset := 0; offset < 4096; offset += 100 {
			chunks, err := s.chunks.List(ctx, run.AccountID, run.EmailID, repository.Page{Limit: 100, Offset: offset})
			if err != nil {
				return nil, err
			}
			for _, c := range chunks {
				texts[c.ID] = categoryClip(c.Text, 160)
			}
			if len(chunks) < 100 {
				break
			}
		}
	}
	visible := min(3, len(sem.Scores))
	if run.UsedClassifier == "semantic" {
		visible = max(visible, len(run.Results))
	}
	for _, score := range sem.Scores[:visible] {
		v := SemanticReviewScore{CategoryID: string(score.CategoryID), CategoryName: shortText(names[score.CategoryID], 100), Score: score.Score, PrototypeHash: score.PrototypeHash, PrototypeStatus: score.PrototypeStatus, ChunkCount: score.ChunkCount, Evidence: []SemanticReviewEvidence{}}
		for _, e := range score.Evidence {
			if texts[e.ChunkID] == "" {
				return nil, repository.ErrConflict
			}
			v.Evidence = append(v.Evidence, SemanticReviewEvidence{e.ChunkID, e.Section, texts[e.ChunkID], e.Similarity, e.Weight})
		}
		dto.Scores = append(dto.Scores, v)
	}
	if _, err := s.results.Get(ctx, run.AccountID, run.EmailID); err != nil {
		return nil, err
	}
	return dto, nil
}
