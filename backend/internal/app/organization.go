package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
)

type OrganizationProvider interface {
	provider.LabelManager
	provider.MetadataReader
	FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error)
	ListLabels(context.Context, provider.PageOptions) (provider.LabelPage, error)
	Capabilities() provider.Capabilities
}

// Organization runs a single bounded job per desktop session. No remote write
// is reachable from list, preview, dry run, status or reconciliation.
type Organization struct {
	prepareChunks func(context.Context, domain.AccountID, domain.ProviderMessageID) error
	riskEngine    *risk.Engine
	mu            sync.Mutex
	gate          chan struct{}
	root          context.Context
	session       func() (domain.AccountID, context.Context, error)
	factory       func(domain.AccountID) OrganizationProvider
	emails        repository.EmailRepository
	categories    repository.CategoryRepository
	results       repository.ClassificationRepository
	journal       repository.OrganizationRepository
	engine        classification.ClassificationEngine
	response      OrganizationResponse
	cancel        context.CancelFunc
	busy          bool
	wg            sync.WaitGroup
}

func NewOrganization(root context.Context, session func() (domain.AccountID, context.Context, error), factory func(domain.AccountID) OrganizationProvider, emails repository.EmailRepository, categories repository.CategoryRepository, results repository.ClassificationRepository, journal repository.OrganizationRepository, engine classification.ClassificationEngine) *Organization {
	return &Organization{riskEngine: risk.DefaultEngine(), root: root, session: session, factory: factory, emails: emails, categories: categories, results: results, journal: journal, engine: engine, response: emptyOrganization()}
}

// SetRiskEngine configures scoring once before any requests.
func (s *Organization) SetRiskEngine(engine *risk.Engine) { s.riskEngine = engine }

// SetChunkProcessor reuses local source preparation after a preview refresh. It
// performs no inference and is configured once before requests begin.
func (s *Organization) SetChunkProcessor(prepare func(context.Context, domain.AccountID, domain.ProviderMessageID) error) {
	s.prepareChunks = prepare
}

// SetOperationGate is configured once before starting jobs; use a capacity-one channel.
func (s *Organization) SetOperationGate(gate chan struct{}) { s.gate = gate }
func (s *Organization) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
func (a *App) Organize(ctx context.Context, p OrganizationRequest) OrganizationResponse {
	if a.Organization == nil {
		r := emptyOrganization()
		r.Code = "unauthorized"
		return r
	}
	return a.Organization.Handle(ctx, p)
}
func orgCode(err error) string {
	switch {
	case errors.Is(err, repository.ErrConflict), errors.Is(err, errRiskBelowThreshold):
		return "stale_preview"
	case errors.Is(err, repository.ErrNotFound):
		return "not_found"
	case errors.Is(err, repository.ErrInvalid):
		return "validation"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(err, provider.ErrAuthenticationRequired):
		return "reauth_required"
	case errors.Is(err, provider.ErrUnsupported):
		return "unsupported"
	default:
		return "operation_failed"
	}
}
func (s *Organization) Handle(ctx context.Context, p OrganizationRequest) OrganizationResponse {
	fail := func(code string) OrganizationResponse { r := emptyOrganization(); r.Code = code; return r }
	if !p.Valid() {
		return fail("validation")
	}
	account, session, err := s.session()
	if err != nil || account == "" || session == nil || session.Err() != nil || p.AccountID != "" && p.AccountID != string(account) {
		return fail("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	if p.Operation == "list" || p.Operation == "suggestions" {
		var result OrganizationResponse
		if p.Operation == "suggestions" {
			result = s.suggestions(ctx, account, domain.EmailID(p.EmailIDs[0]))
		} else {
			result = s.list(ctx, account, p.Offset, p.Source, p.SuggestionMode == "highest")
		}
		if session.Err() != nil {
			return fail("unauthorized")
		}
		return result
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Operation == "cancel" {
		if s.cancel != nil {
			s.cancel()
		}
		r := s.response
		if r.AccountID != string(account) {
			r = emptyOrganization()
		}
		return r
	}
	if s.busy {
		if s.response.AccountID == string(account) {
			return s.response
		}
		return fail("busy")
	}
	if p.Operation == "status" {
		// Persistent recovery survives restart and a new session. No cached personal
		// content from a different account is returned.
		if s.response.AccountID == string(account) && (s.response.State == "dry_run" || s.response.Code != "") {
			return s.response
		}
		plan, err := s.journal.Latest(ctx, account, false)
		if errors.Is(err, repository.ErrNotFound) {
			r := emptyOrganization()
			r.AccountID = string(account)
			return r
		}
		if err != nil {
			return fail(orgCode(err))
		}
		s.response = organizationDTO(plan)
		s.response = s.withCachedLabels(ctx, s.response)
		if s.response.State == "preview" && time.Now().UnixMilli() >= plan.ExpiresAt {
			s.response.State = "expired"
		}
		return s.response
	}
	// Only preview/reconcile/apply use the network, outside the short IPC call.
	worker, cancelWorker := context.WithTimeout(s.root, 3*time.Minute)
	s.cancel = cancelWorker
	stopSession := context.AfterFunc(session, cancelWorker)
	s.busy = true
	s.response = emptyOrganization()
	s.response.AccountID = string(account)
	s.response.State = "running"
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancelWorker()
		defer stopSession()
		var result OrganizationResponse
		var err error
		if s.gate != nil {
			select {
			case s.gate <- struct{}{}:
				defer func() { <-s.gate }()
			case <-worker.Done():
				err = worker.Err()
			}
		}
		if err == nil {
			result, err = s.work(worker, account, p)
		} else {
			result = emptyOrganization()
		}
		result = s.withCachedLabels(worker, result)

		result.AccountID = string(account)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			result.Code = orgCode(err)
			if result.State == "idle" {
				result.State = "error"
			}
		}
		if session.Err() != nil {
			result = emptyOrganization()
			result.Code = "unauthorized"
		}
		s.response = result
		s.busy = false
	}()
	return s.response
}
func (s *Organization) work(ctx context.Context, account domain.AccountID, request OrganizationRequest) (OrganizationResponse, error) {
	p := s.factory(account)
	caps := p.Capabilities()
	if !caps.ModifyLabels || !caps.CreateLabels {
		return emptyOrganization(), provider.ErrUnsupported
	}
	latest, err := s.journal.Latest(ctx, account, false)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return emptyOrganization(), err
	}
	if err == nil && (latest.Status == domain.ActionApplying || latest.Status == domain.ActionUncertain) {
		if request.Operation != "reconcile" || (request.ActionID != "" && request.ActionID != string(latest.ID)) {
			r := organizationDTO(latest)
			r.Code = "reconciliation_required"
			return r, nil
		}
		return s.reconcile(ctx, latest, p)
	}
	switch request.Operation {
	case "preview", "dry_run":
		return s.preview(ctx, account, request, p)
	case "undo_preview":
		return s.undoPreview(ctx, account, p)
	case "apply":
		plan, err := s.journal.Get(ctx, account, domain.ActionID(request.ActionID))
		if err != nil {
			return emptyOrganization(), err
		}
		if plan.Digest != request.Digest || plan.Status != domain.ActionPreview || time.Now().UnixMilli() >= plan.ExpiresAt {
			return organizationDTO(plan), repository.ErrConflict
		}
		return s.apply(ctx, plan, p)
	case "reconcile":
		return organizationDTO(latest), nil
	default:
		return emptyOrganization(), repository.ErrInvalid
	}
}
