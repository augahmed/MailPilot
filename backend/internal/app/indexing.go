package app

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/model"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
)

type IndexRequest struct {
	Operation string `json:"operation"`
	AccountID string `json:"accountId"`
}

func (r IndexRequest) Valid() bool {
	if len(r.AccountID) > 100 {
		return false
	}
	switch r.Operation {
	case "status":
		return true
	case "start", "pause", "resume", "cancel", "rebuild":
		return r.AccountID != ""
	default:
		return false
	}
}

type IndexResponse struct {
	AssessedMessages    int    `json:"assessedMessages"`
	PendingAssessments  int    `json:"pendingAssessments"`
	EligibleCategories  int    `json:"eligibleCategories"`
	ReadyCategories     int    `json:"readyCategories"`
	PartialCategories   int    `json:"partialCategories"`
	PendingCategories   int    `json:"pendingCategories"`
	AccountID           string `json:"accountId"`
	Status              string `json:"status"`
	Generation          string `json:"generation"`
	ModelID             string `json:"modelId"`
	ModelVersion        string `json:"modelVersion"`
	Fingerprint         string `json:"fingerprint"`
	Dimension           int    `json:"dimension"`
	EligibleMessages    int    `json:"eligibleMessages"`
	IndexedMessages     int    `json:"indexedMessages"`
	CompleteMessages    int    `json:"completeMessages"`
	PartialMessages     int    `json:"partialMessages"`
	FailedMessages      int    `json:"failedMessages"`
	PendingMessages     int    `json:"pendingMessages"`
	UnavailableMessages int    `json:"unavailableMessages"`
	ChunksCreated       int    `json:"chunksCreated"`
	EmbeddingsGenerated int    `json:"embeddingsGenerated"`
	Code                string `json:"code"`
}
type localEmbedder interface {
	Descriptor() model.Descriptor
	EmbedDocument(context.Context, []string) ([][]float32, error)
	Close(context.Context) error
}
type indexWorker struct {
	run    repository.IndexRun
	cancel context.CancelFunc
	done   chan struct{}
}

// LocalIndex owns one explicitly started local job, never Gmail or external AI.
// SQLite state is the resume checkpoint; vectors commit in at most four-item batches.
type LocalIndex struct {
	root            context.Context
	session         func() (domain.AccountID, context.Context, error)
	emails          repository.EmailRepository
	chunks          repository.ChunkRepository
	jobs            repository.IndexRepository
	categoryVectors repository.CategoryVectorRepository
	semantic        repository.SemanticRepository
	classifier      *classification.SemanticClassifier
	embedder        localEmbedder
	chunker         *mailtext.Chunker
	profile         repository.IndexProfile
	mu              sync.Mutex
	worker          *indexWorker
	closed          bool
}

func NewLocalIndex(root context.Context, session func() (domain.AccountID, context.Context, error), emails repository.EmailRepository, chunks repository.ChunkRepository, jobs repository.IndexRepository, provider localEmbedder) (*LocalIndex, error) {
	chunker, err := mailtext.NewChunker()
	if err != nil {
		return nil, err
	}
	d := provider.Descriptor()
	profile := repository.IndexProfile{Model: repository.EmbeddingModel{ID: d.ModelID, Version: d.Version, Dimension: d.Dimensions}, PreprocessingVersion: mailtext.PreprocessingVersion, IndexVersion: mailtext.ChunkIndexVersion, Fingerprint: d.PreprocessingFingerprint}
	if err = jobs.Recover(root); err != nil {
		return nil, err
	}
	return &LocalIndex{root: root, session: session, emails: emails, chunks: chunks, jobs: jobs, embedder: provider, chunker: chunker, profile: profile}, nil
}
func (a *App) ManageIndex(ctx context.Context, p IndexRequest) IndexResponse {
	if a.Index == nil {
		return IndexResponse{Status: "idle", Code: "unauthorized"}
	}
	return a.Index.Manage(ctx, p)
}
func (s *LocalIndex) Manage(ctx context.Context, p IndexRequest) IndexResponse {
	if !p.Valid() {
		return IndexResponse{Status: "idle", Code: "validation"}
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || p.AccountID != "" && p.AccountID != string(account) {
		return IndexResponse{Status: "idle", Code: "unauthorized"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return IndexResponse{Status: "idle", Code: "unavailable"}
	}
	if p.Operation != "status" {
		switch p.Operation {
		case "pause", "cancel":
			run, e := s.jobs.Run(ctx, account)
			if e == nil && (run.Status == "running" || p.Operation == "cancel") {
				status := "paused"
				if p.Operation == "cancel" {
					status = "cancelled"
				}
				err = s.jobs.Transition(ctx, run, status, "")
				if errors.Is(err, repository.ErrNotFound) {
					err = nil
				}
			} else if e != nil && !errors.Is(e, repository.ErrNotFound) {
				err = e
			}
			if s.worker != nil && s.worker.run.AccountID == account {
				s.worker.cancel()
			}
		default:
			if s.worker != nil {
				select {
				case <-s.worker.done:
					s.worker = nil
				default:
					run, e := s.jobs.Run(ctx, account)
					if e == nil && run.Status == "running" && p.Operation != "rebuild" {
						s.mu.Unlock()
						return s.authorizedResponse(ctx, account, session, "")
					}
					s.worker.cancel()
					select {
					case <-s.worker.done:
						s.worker = nil
					case <-ctx.Done():
						s.mu.Unlock()
						return s.authorizedResponse(ctx, account, session, "busy")
					}
				}
			}
			if ctx.Err() == nil {
				var run repository.IndexRun
				run, err = s.jobs.Begin(ctx, account, s.profile, p.Operation == "rebuild")
				if err == nil && s.semantic != nil {
					run, err = s.semantic.Prepare(ctx, run, s.classifier.Version())
				}
				if err == nil {
					jobCtx, jobCancel := context.WithCancel(s.root)
					sessionStop := context.AfterFunc(session, jobCancel)
					w := &indexWorker{run: run, cancel: jobCancel, done: make(chan struct{})}
					s.worker = w
					go func() { defer close(w.done); defer jobCancel(); defer sessionStop(); s.work(jobCtx, run) }()
				}
			}
		}
	}
	s.mu.Unlock()
	if session.Err() != nil {
		return IndexResponse{Status: "idle", Code: "unauthorized"}
	}
	code := ""
	if err != nil {
		code = "storage_error"
	}
	return s.authorizedResponse(ctx, account, session, code)
}
func (s *LocalIndex) authorizedResponse(ctx context.Context, a domain.AccountID, session context.Context, code string) IndexResponse {
	result := s.response(ctx, a, code)
	if session.Err() != nil {
		return IndexResponse{Status: "idle", Code: "unauthorized"}
	}
	return result
}
func (s *LocalIndex) response(ctx context.Context, a domain.AccountID, code string) IndexResponse {
	p := s.profile
	result := IndexResponse{AccountID: string(a), Status: "idle", ModelID: p.Model.ID, ModelVersion: p.Model.Version, Dimension: p.Model.Dimension, Fingerprint: p.Fingerprint, Code: code}
	classifier := ""
	if s.semantic != nil {
		classifier = s.classifier.Version()
	}
	coverage, err := s.jobs.Coverage(ctx, a, p, classifier)
	if err != nil {
		result.Code = "storage_error"
		return result
	}
	if run := coverage.Run; run != nil {
		result.Status = run.Status
		result.Generation = run.Generation
		// Category work adds pending derivatives without invalidating compatible mail.
		mailProfile := run.Profile
		mailProfile.CategoryRecipe = p.CategoryRecipe
		if mailProfile != p {
			result.Status = "interrupted"
			result.Code = "model_changed"
			if run.Profile.Model == p.Model && run.Profile.Fingerprint == p.Fingerprint {
				result.Code = "preprocessing_changed"
			}
		} else if code == "" {
			result.Code = run.Code
		}
	}
	counts := coverage.Messages
	result.EligibleMessages = counts.Eligible
	result.IndexedMessages = counts.Indexed
	result.CompleteMessages = counts.Complete
	result.PartialMessages = counts.Partial
	result.FailedMessages = counts.Failed
	result.PendingMessages = counts.Pending
	result.UnavailableMessages = counts.Unavailable
	result.ChunksCreated = counts.Chunks
	result.EmbeddingsGenerated = counts.Embeddings
	if s.categoryVectors != nil {
		c := coverage.Categories
		result.EligibleCategories = c.Eligible
		result.ReadyCategories = c.Ready
		result.PartialCategories = c.Partial
		result.PendingCategories = c.Pending
	}
	if s.semantic != nil {
		semantic := coverage.Assessments
		result.AssessedMessages = semantic.Assessed
		result.PendingAssessments = semantic.Pending
	}
	if result.Status == "complete" || result.Status == "partial" {
		switch {
		case counts.Pending > 0 || counts.Embeddings < counts.Chunks || result.PendingCategories > 0 || result.PendingAssessments > 0:
			result.Code = "pending_changes"
			result.Status = "pending"
		case counts.Partial > 0 || counts.Failed > 0 || counts.Unavailable > 0 || result.PartialCategories > 0:
			result.Status = "partial"
		default:
			result.Status = "complete"
		}
	}
	return result
}
func (s *LocalIndex) work(ctx context.Context, run repository.IndexRun) {
	defer func() {
		if ctx.Err() != nil {
			end, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.jobs.Transition(end, run, "interrupted", "interrupted")
		}
	}()
	for ctx.Err() == nil {
		candidates, err := s.jobs.Next(ctx, run)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, repository.ErrNotFound) {
				s.finish(run, "failed", "storage_error")
			}
			return
		}
		if len(candidates) == 0 {
			if s.categoryVectors != nil {
				categories, e := s.categoryVectors.Next(ctx, run)
				if e != nil {
					if ctx.Err() == nil {
						s.finish(run, "failed", "storage_error")
					}
					return
				}
				if len(categories) > 0 {
					e = s.indexCategory(ctx, run, categories[0])
					if e == nil || errors.Is(e, repository.ErrConflict) || errors.Is(e, repository.ErrNotFound) {
						continue
					}
					if ctx.Err() != nil {
						return
					}
					code := "inference_failed"
					if errors.Is(e, embedding.ErrUnavailable) {
						code = "model_unavailable"
					}
					if errors.Is(e, repository.ErrStorage) {
						code = "storage_error"
					}
					s.finish(run, "failed", code)
					return
				}
			}
			if s.semantic != nil {
				ids, e := s.semantic.Next(ctx, run, s.classifier.Version())
				if e != nil {
					if ctx.Err() == nil {
						s.finish(run, "failed", "storage_error")
					}
					return
				}
				if len(ids) > 0 {
					snapshot, e := s.semantic.Snapshot(ctx, run.AccountID, ids[0], run.Profile)
					if e == nil {
						var assessment domain.ClassificationRun
						assessment, e = s.classifier.Assess(ctx, snapshot)
						if e == nil {
							e = s.semantic.SaveIndexed(ctx, run, assessment)
						}
					}
					if e == nil || errors.Is(e, repository.ErrConflict) || errors.Is(e, repository.ErrNotFound) {
						continue
					}
					if ctx.Err() == nil {
						s.finish(run, "failed", "storage_error")
					}
					return
				}
			}
			counts, err := s.jobs.Progress(ctx, run.AccountID, run.Profile)
			if err != nil {
				s.finish(run, "failed", "storage_error")
				return
			}
			status := "complete"
			if counts.Partial > 0 || counts.Failed > 0 || counts.Unavailable > 0 {
				status = "partial"
			}
			err = s.jobs.Transition(ctx, run, status, "")
			if errors.Is(err, repository.ErrConflict) {
				continue
			}
			if err != nil && ctx.Err() == nil {
				s.finish(run, "failed", "storage_error")
			}
			return
		}
		for _, candidate := range candidates {
			if ctx.Err() != nil {
				return
			}
			err = s.indexMessage(ctx, run, candidate)
			if err == nil || errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrNotFound) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, mailtext.ErrContent) || errors.Is(err, embedding.ErrInput) {
				if e := s.jobs.Fail(ctx, run, candidate, "invalid_content"); e != nil && !errors.Is(e, repository.ErrConflict) && !errors.Is(e, repository.ErrNotFound) {
					s.finish(run, "failed", "storage_error")
					return
				}
				continue
			}
			if errors.Is(err, embedding.ErrUnavailable) {
				s.finish(run, "failed", "model_unavailable")
				return
			}
			if errors.Is(err, embedding.ErrInference) || errors.Is(err, embedding.ErrVector) || errors.Is(err, context.DeadlineExceeded) {
				code := "inference_failed"
				if errors.Is(err, embedding.ErrVector) {
					code = "invalid_vector"
				}
				_ = s.jobs.Fail(ctx, run, candidate, code)
				s.finish(run, "failed", "inference_failed")
				return
			}
			s.finish(run, "failed", "storage_error")
			return
		}
	}
}
func (s *LocalIndex) finish(run repository.IndexRun, status, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.jobs.Transition(ctx, run, status, code)
}
func (s *LocalIndex) indexMessage(ctx context.Context, run repository.IndexRun, c repository.IndexCandidate) error {
	state, err := s.chunks.State(ctx, run.AccountID, c.EmailID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if err != nil || state.Status == repository.IndexStale || state.Model != run.Profile.Model || state.PreprocessingVersion != run.Profile.PreprocessingVersion || state.IndexVersion != run.Profile.IndexVersion {
		stored, err := s.emails.Get(ctx, run.AccountID, c.EmailID)
		if err != nil {
			return err
		}
		if stored.ContentHash != c.SourceRevision {
			return repository.ErrConflict
		}
		snapshot, err := s.chunker.Build(ctx, stored)
		if err != nil {
			return err
		}
		snapshot.Model = run.Profile.Model
		state, err = s.jobs.ReplaceIndexed(ctx, run, snapshot)
		if err != nil {
			return err
		}
	}
	var batch []repository.EmailChunk
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = mailtext.ModelInput(c.ChunkInput)
		}
		vectors, err := s.embedder.EmbedDocument(ctx, texts)
		if err != nil {
			return err
		}
		if len(vectors) != len(batch) {
			return embedding.ErrVector
		}
		write := repository.EmbeddingBatch{State: state}
		for i, c := range batch {
			if !validIndexVector(vectors[i], run.Profile.Model.Dimension) {
				return embedding.ErrVector
			}
			write.Embeddings = append(write.Embeddings, repository.ChunkEmbedding{ChunkID: c.ID, Status: repository.EmbeddingReady, Vector: vectors[i]})
		}
		state, err = s.jobs.SaveIndexed(ctx, run, write)
		batch = nil
		return err
	}
	for offset := 0; ; offset += 100 {
		chunks, err := s.chunks.List(ctx, run.AccountID, c.EmailID, repository.Page{Limit: 100, Offset: offset})
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			vector, err := s.chunks.Embedding(ctx, run.AccountID, c.EmailID, chunk.ID, run.Profile.Model)
			if err != nil {
				return err
			}
			if vector.Status == repository.EmbeddingReady {
				continue
			}
			batch = append(batch, chunk)
			if len(batch) == embedding.MaxBatch {
				if err = flush(); err != nil {
					return err
				}
			}
		}
		if len(chunks) < 100 {
			break
		}
	}
	return flush()
}
func validIndexVector(v []float32, dim int) bool {
	if len(v) != dim {
		return false
	}
	norm := 0.0
	for _, f := range v {
		x := float64(f)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
		norm += x * x
	}
	return math.Abs(norm-1) <= 1e-4
}

// Clear revokes current work on session changes. It never starts inference.
func (s *LocalIndex) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.worker != nil {
		s.worker.cancel()
	}
}
func (s *LocalIndex) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	w := s.worker
	if w != nil {
		w.cancel()
	}
	s.mu.Unlock()
	if w != nil {
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.embedder.Close(ctx)
}

// EnableClassification configures dependent assessment work on the existing
// coordinator before any operation. No classification-specific worker is created.
func (s *LocalIndex) EnableClassification(repo repository.SemanticRepository, engine *classification.SemanticClassifier) {
	s.semantic = repo
	s.classifier = engine
}
