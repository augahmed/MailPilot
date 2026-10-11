package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

type OrganizationProvider interface {
	provider.LabelManager
	provider.MetadataReader
	FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error)
	ListLabels(context.Context, provider.PageOptions) (provider.LabelPage, error)
	Capabilities() provider.Capabilities
}
type OrganizationRequest struct {
	Operation string   `json:"operation"`
	AccountID string   `json:"accountId"`
	Offset    int      `json:"offset"`
	EmailIDs  []string `json:"emailIds"`
	ActionID  string   `json:"actionId"`
	Digest    string   `json:"digest"`
}

func (p OrganizationRequest) Valid() bool {
	if len(p.AccountID) > 100 || p.Offset < 0 || p.Offset > 1000000 || len(p.EmailIDs) > 5 || len(p.ActionID) > 100 || len(p.Digest) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range p.EmailIDs {
		if id == "" || len(id) > 100 || seen[id] {
			return false
		}
		seen[id] = true
	}
	switch p.Operation {
	case "list", "status":
		return len(p.EmailIDs) == 0 && p.Digest == ""
	case "preview", "dry_run":
		return p.AccountID != "" && len(p.EmailIDs) > 0 && p.ActionID == "" && p.Digest == ""
	case "undo_preview":
		return p.AccountID != "" && len(p.EmailIDs) == 0 && p.ActionID == "" && p.Digest == ""
	case "apply":
		return p.AccountID != "" && p.ActionID != "" && len(p.Digest) == 64 && len(p.EmailIDs) == 0
	case "reconcile", "cancel":
		return p.AccountID != "" && len(p.EmailIDs) == 0 && p.Digest == ""
	default:
		return false
	}
}

type OrganizationRowDTO struct {
	EmailID       string   `json:"emailId"`
	Sender        string   `json:"sender"`
	Subject       string   `json:"subject"`
	CurrentLabels []string `json:"currentLabels"`
	ProposedLabel string   `json:"proposedLabel"`
	Confidence    float64  `json:"confidence"`
	Change        string   `json:"change"`
}
type OrganizationStepDTO struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	EmailID string `json:"emailId"`
	State   string `json:"state"`
	Code    string `json:"code"`
}
type OrganizationResponse struct {
	AccountID string                `json:"accountId"`
	State     string                `json:"state"`
	Code      string                `json:"code"`
	ID        string                `json:"id"`
	Digest    string                `json:"digest"`
	Kind      string                `json:"kind"`
	ExpiresAt int64                 `json:"expiresAt"`
	DryRun    bool                  `json:"dryRun"`
	Rows      []OrganizationRowDTO  `json:"rows"`
	Steps     []OrganizationStepDTO `json:"steps"`
	HasMore   bool                  `json:"hasMore"`
}

func emptyOrganization() OrganizationResponse {
	return OrganizationResponse{State: "idle", Rows: []OrganizationRowDTO{}, Steps: []OrganizationStepDTO{}}
}

// Account-scoped display fields are hostile input. Bound the encoded response,
// including JSON escaping, before it reaches the native frame reader.
func boundedOrganization(r OrganizationResponse) OrganizationResponse {
	data, err := json.Marshal(r)
	if err != nil || len(data) > 48*1024 {
		limited := emptyOrganization()
		limited.AccountID = r.AccountID
		limited.Code = "validation"
		return limited
	}
	return r
}
func organizationDTO(p domain.OrganizationPlan) OrganizationResponse {
	r := emptyOrganization()
	r.AccountID = string(p.AccountID)
	r.State = string(p.Status)
	r.ID = string(p.ID)
	r.Digest = p.Digest
	r.Kind = string(p.Kind)
	r.ExpiresAt = p.ExpiresAt
	for _, row := range p.Rows {
		r.Rows = append(r.Rows, OrganizationRowDTO{string(row.EmailID), row.Sender, row.Subject, row.CurrentLabels, row.ProposedLabel, row.Confidence, row.Change})
	}
	for _, step := range p.Steps {
		r.Steps = append(r.Steps, OrganizationStepDTO{step.Kind, step.Name, string(step.EmailID), string(step.State), step.Code})
	}
	return boundedOrganization(r)
}

// Organization runs a single bounded job per desktop session. No remote write
// is reachable from list, preview, dry run, status or reconciliation.
type Organization struct {
	mu         sync.Mutex
	gate       chan struct{}
	root       context.Context
	session    func() (domain.AccountID, context.Context, error)
	factory    func(domain.AccountID) OrganizationProvider
	emails     repository.EmailRepository
	categories repository.CategoryRepository
	results    repository.ClassificationRepository
	journal    repository.OrganizationRepository
	engine     classification.ClassificationEngine
	response   OrganizationResponse
	cancel     context.CancelFunc
	busy       bool
	wg         sync.WaitGroup
}

func NewOrganization(root context.Context, session func() (domain.AccountID, context.Context, error), factory func(domain.AccountID) OrganizationProvider, emails repository.EmailRepository, categories repository.CategoryRepository, results repository.ClassificationRepository, journal repository.OrganizationRepository, engine classification.ClassificationEngine) *Organization {
	return &Organization{root: root, session: session, factory: factory, emails: emails, categories: categories, results: results, journal: journal, engine: engine, response: emptyOrganization()}
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
func (s *Organization) Busy() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.busy }
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
	case errors.Is(err, repository.ErrConflict):
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
	if p.Operation == "list" {
		return s.list(ctx, account, p.Offset)
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
func (s *Organization) list(ctx context.Context, account domain.AccountID, offset int) OrganizationResponse {
	r := emptyOrganization()
	r.AccountID = string(account)
	emails, err := s.emails.List(ctx, account, repository.Page{Limit: 6, Offset: offset})
	if err != nil {
		r.Code = orgCode(err)
		return r
	}
	r.HasMore = len(emails) > 5
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		r.Code = orgCode(err)
		return r
	}
	names := map[domain.CategoryID]string{}
	for _, c := range categories {
		if c.Enabled {
			names[c.ID] = c.Name
		}
	}
	for _, stored := range emails[:min(5, len(emails))] {
		email := stored.Email
		row := OrganizationRowDTO{EmailID: string(email.ID), Sender: shortText(email.Sender.Address, 100), Subject: shortText(email.Subject, 200), CurrentLabels: []string{}, Change: "unclassified"}
		for _, label := range email.Labels[:min(len(email.Labels), 50)] {
			row.CurrentLabels = append(row.CurrentLabels, shortText(label.Name, 100))
		}
		run, err := s.results.Get(ctx, account, email.ID)
		if err == nil && run.ClassifierName == s.engine.Name() && run.ClassifierVersion == s.engine.Version() && len(run.Results) == 1 {
			prediction := run.Results[0]
			name, ok := names[prediction.CategoryID]
			if ok {
				row.ProposedLabel = "MailPilot/" + name
				row.Confidence = prediction.Confidence
				row.Change = "candidate"
			}
		}
		r.Rows = append(r.Rows, row)
	}
	return boundedOrganization(r)
}
func labelsSnapshot(account domain.AccountID, labels []domain.EmailLabel) ([]domain.ProviderLabelID, []string, error) {
	if len(labels) > 50 {
		return nil, nil, repository.ErrInvalid
	}
	ids := []domain.ProviderLabelID{}
	names := []string{}
	seen := map[domain.ProviderLabelID]bool{}
	for _, l := range labels {
		if l.AccountID != account || l.ProviderID == "" || seen[l.ProviderID] || len(l.Name) > 200 {
			return nil, nil, provider.ErrMalformed
		}
		seen[l.ProviderID] = true
		ids = append(ids, l.ProviderID)
		names = append(names, l.Name)
	}
	slices.Sort(ids)
	slices.Sort(names)
	return ids, names, nil
}
func listOrganizationLabels(ctx context.Context, account domain.AccountID, p OrganizationProvider) (map[string]domain.EmailLabel, error) {
	labels := map[string]domain.EmailLabel{}
	tokens := map[string]bool{}
	token := ""
	for page := 0; page < 100; page++ {
		if tokens[token] {
			return nil, provider.ErrMalformed
		}
		tokens[token] = true
		result, err := p.ListLabels(ctx, provider.PageOptions{Limit: 100, PageToken: token})
		if err != nil {
			return nil, err
		}
		if len(result.Labels) > 100 {
			return nil, provider.ErrMalformed
		}
		for _, l := range result.Labels {
			if l.AccountID != account || l.ProviderID == "" {
				return nil, provider.ErrMalformed
			}
			if _, exists := labels[l.Name]; exists {
				return nil, repository.ErrConflict
			}
			labels[l.Name] = l
		}
		if result.NextPageToken == "" {
			return labels, nil
		}
		token = result.NextPageToken
	}
	return nil, repository.ErrInvalid
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
func (s *Organization) prediction(ctx context.Context, account domain.AccountID, id domain.EmailID) (domain.ClassificationRun, domain.EmailCategory, domain.Email, error) {
	run, err := s.results.Get(ctx, account, id)
	if err != nil {
		return run, domain.EmailCategory{}, domain.Email{}, err
	}
	if run.ClassifierName != s.engine.Name() || run.ClassifierVersion != s.engine.Version() || len(run.Results) != 1 {
		return run, domain.EmailCategory{}, domain.Email{}, repository.ErrConflict
	}
	cat, err := s.categories.Get(ctx, account, run.Results[0].CategoryID)
	if err != nil {
		return run, cat, domain.Email{}, err
	}
	if !cat.Enabled || strings.ContainsFunc(cat.Name, unicode.IsControl) {
		return run, cat, domain.Email{}, repository.ErrInvalid
	}
	email, err := s.emails.Get(ctx, account, id)
	return run, cat, email.Email, err
}
func (s *Organization) preview(ctx context.Context, account domain.AccountID, req OrganizationRequest, p OrganizationProvider) (OrganizationResponse, error) {
	labels, err := listOrganizationLabels(ctx, account, p)
	if err != nil {
		return emptyOrganization(), err
	}
	categories, err := s.categories.List(ctx, account)
	if err != nil {
		return emptyOrganization(), err
	}
	plan := domain.OrganizationPlan{AccountID: account, Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{}, Steps: []domain.OrganizationStep{}}
	creates := map[string]bool{}
	for _, id := range req.EmailIDs {
		run, cat, email, err := s.prediction(ctx, account, domain.EmailID(id))
		if err != nil {
			return emptyOrganization(), err
		}
		live, err := p.FetchMessage(ctx, email.ProviderID)
		if err != nil {
			return emptyOrganization(), err
		}
		if live.AccountID != account || live.ProviderID != email.ProviderID || classification.InputHash(live, categories) != run.InputHash {
			return emptyOrganization(), repository.ErrConflict
		}
		before, names, err := labelsSnapshot(account, live.Labels)
		if err != nil {
			return emptyOrganization(), err
		}
		name := "MailPilot/" + cat.Name
		row := domain.OrganizationRow{EmailID: email.ID, ProviderID: email.ProviderID, CategoryID: cat.ID, InputHash: run.InputHash, PredictionTimestamp: run.Timestamp.UnixMilli(), Sender: shortText(email.Sender.Address, 100), Subject: shortText(email.Subject, 200), CurrentLabels: names, BeforeLabels: before, ProposedLabel: name, Confidence: run.Results[0].Confidence, Change: "add"}
		label, exists := labels[name]
		if exists {
			if label.Kind != domain.LabelKindUser {
				return emptyOrganization(), repository.ErrConflict
			}
			row.LabelID = label.ProviderID
			if slices.Contains(before, label.ProviderID) {
				row.Change = "none"
			}
		}
		if !exists && !creates[name] {
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "create_label", Name: name, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
			creates[name] = true
		}
		plan.Rows = append(plan.Rows, row)
	}
	// Creation steps precede all memberships and shared categories create once.
	for _, row := range plan.Rows {
		if row.Change == "add" {
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "add", Name: row.ProposedLabel, EmailID: row.EmailID, LabelID: row.LabelID, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
		}
	}
	if organizationDTO(plan).Code != "" {
		return emptyOrganization(), repository.ErrInvalid
	}
	if req.Operation == "dry_run" {
		r := organizationDTO(plan)
		r.State = "dry_run"
		r.DryRun = true
		return r, nil
	}
	saved, err := s.journal.Create(ctx, plan)
	return organizationDTO(saved), err
}
func (s *Organization) undoPreview(ctx context.Context, account domain.AccountID, p OrganizationProvider) (OrganizationResponse, error) {
	original, err := s.journal.Latest(ctx, account, true)
	if err != nil {
		return emptyOrganization(), err
	}
	plan := domain.OrganizationPlan{AccountID: account, Kind: domain.ActionUndo, Reverses: original.ID, Rows: []domain.OrganizationRow{}, Steps: []domain.OrganizationStep{}}
	for _, step := range original.Steps {
		if step.Kind != "add" || step.State != domain.ItemApplied || !step.Added {
			continue
		}
		row := findOrganizationRow(original, step.EmailID)
		live, err := p.FetchMessageMetadata(ctx, row.ProviderID)
		if err != nil {
			return emptyOrganization(), err
		}
		if live.ProviderID != row.ProviderID {
			return emptyOrganization(), provider.ErrMalformed
		}
		ids, names, err := labelsSnapshot(account, live.Labels)
		if err != nil {
			return emptyOrganization(), err
		}
		row.BeforeLabels = ids
		row.CurrentLabels = names
		row.LabelID = step.ResultID
		row.Change = "none"
		if slices.Contains(ids, step.ResultID) {
			row.Change = "remove"
			plan.Steps = append(plan.Steps, domain.OrganizationStep{Kind: "remove", EmailID: row.EmailID, Name: row.ProposedLabel, LabelID: step.ResultID, State: domain.ItemPending, AfterLabels: []domain.ProviderLabelID{}})
		}
		plan.Rows = append(plan.Rows, row)
	}
	if len(plan.Rows) == 0 {
		return emptyOrganization(), repository.ErrNotFound
	}
	saved, err := s.journal.Create(ctx, plan)
	return organizationDTO(saved), err
}
func findOrganizationRow(p domain.OrganizationPlan, id domain.EmailID) domain.OrganizationRow {
	for _, row := range p.Rows {
		if row.EmailID == id {
			return row
		}
	}
	return domain.OrganizationRow{}
}

// Durability must outlive cancellation after dispatch. These bounded DB-only
// writes do not authorize further network activity or a disconnected account.
func (s *Organization) save(p domain.OrganizationPlan) (domain.OrganizationPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.journal.Update(ctx, p)
}
func (s *Organization) finish(p domain.OrganizationPlan) (OrganizationResponse, error) {
	unresolved, applied, failed := false, 0, 0
	cancelled := false
	for _, step := range p.Steps {
		switch step.State {
		case domain.ItemInFlight, domain.ItemUncertain:
			unresolved = true
		case domain.ItemApplied:
			applied++
		case domain.ItemSkipped:
			if step.Code == "cancelled" || step.Code == "recovered_pending" {
				cancelled = true
			}
		default:
			failed++
		}
	}
	if unresolved {
		p.Status = domain.ActionUncertain
	} else if cancelled {
		p.Status = domain.ActionCancelled
	} else if failed == 0 {
		p.Status = domain.ActionApplied
	} else if applied > 0 {
		p.Status = domain.ActionPartial
	} else {
		p.Status = domain.ActionFailed
	}
	p, err := s.save(p)
	return organizationDTO(p), err
}
func (s *Organization) apply(ctx context.Context, plan domain.OrganizationPlan, p OrganizationProvider) (OrganizationResponse, error) {
	// Revalidate the complete plan before creating any label or dispatching writes.
	if plan.Kind == domain.ActionUndo {
		latest, err := s.journal.Latest(ctx, plan.AccountID, true)
		if err != nil {
			return organizationDTO(plan), err
		}
		if latest.ID != plan.Reverses {
			return organizationDTO(plan), repository.ErrConflict
		}
	}
	labels, err := listOrganizationLabels(ctx, plan.AccountID, p)
	if err != nil {
		return organizationDTO(plan), err
	}
	categories, err := s.categories.List(ctx, plan.AccountID)
	if err != nil {
		return organizationDTO(plan), err
	}
	for _, row := range plan.Rows {
		if plan.Kind == domain.ActionLabelDelta {
			run, cat, _, err := s.prediction(ctx, plan.AccountID, row.EmailID)
			if err != nil {
				return organizationDTO(plan), err
			}
			if run.InputHash != row.InputHash || run.Timestamp.UnixMilli() != row.PredictionTimestamp || "MailPilot/"+cat.Name != row.ProposedLabel {
				return organizationDTO(plan), repository.ErrConflict
			}
			live, err := p.FetchMessage(ctx, row.ProviderID)
			if err != nil {
				return organizationDTO(plan), err
			}
			if live.AccountID != plan.AccountID || live.ProviderID != row.ProviderID || classification.InputHash(live, categories) != row.InputHash {
				return organizationDTO(plan), repository.ErrConflict
			}
			ids, _, err := labelsSnapshot(plan.AccountID, live.Labels)
			if err != nil || !slices.Equal(ids, row.BeforeLabels) {
				return organizationDTO(plan), repository.ErrConflict
			}
		}
		l, exists := labels[row.ProposedLabel]
		if row.LabelID != "" {
			if !exists || l.Kind != domain.LabelKindUser || l.ProviderID != row.LabelID {
				return organizationDTO(plan), repository.ErrConflict
			}
		} else if exists {
			return organizationDTO(plan), repository.ErrConflict
		}
	}
	if ctx.Err() != nil {
		return organizationDTO(plan), ctx.Err()
	}
	plan.Status = domain.ActionApplying
	plan, err = s.save(plan)
	if err != nil {
		return organizationDTO(plan), err
	}
	resolved := map[string]domain.ProviderLabelID{}
	for _, row := range plan.Rows {
		if row.LabelID != "" {
			resolved[row.ProposedLabel] = row.LabelID
		}
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if ctx.Err() != nil {
			step.State = domain.ItemSkipped
			step.Code = "cancelled"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		if step.Kind != "create_label" && resolved[step.Name] == "" {
			step.State = domain.ItemConflict
			step.Code = "label_unresolved"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		var before []domain.ProviderLabelID
		if step.Kind != "create_label" {
			row := findOrganizationRow(plan, step.EmailID)
			if plan.Kind == domain.ActionLabelDelta {
				run, cat, _, checkErr := s.prediction(ctx, plan.AccountID, row.EmailID)
				if checkErr != nil || run.InputHash != row.InputHash || run.Timestamp.UnixMilli() != row.PredictionTimestamp || "MailPilot/"+cat.Name != row.ProposedLabel {
					step.State = domain.ItemConflict
					step.Code = "prediction_changed"
					plan, err = s.save(plan)
					if err != nil {
						return organizationDTO(plan), err
					}
					continue
				}
			}
			live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
			if readErr == nil && plan.Kind == domain.ActionLabelDelta {
				full, fullErr := p.FetchMessage(ctx, row.ProviderID)
				if fullErr != nil {
					readErr = fullErr
				} else if full.AccountID != plan.AccountID || full.ProviderID != row.ProviderID || classification.InputHash(full, categories) != row.InputHash {
					readErr = repository.ErrConflict
				} else {
					live.Labels = full.Labels
				}
			}
			if readErr == nil && live.ProviderID != row.ProviderID {
				readErr = provider.ErrMalformed
			}
			if readErr == nil {
				before, _, readErr = labelsSnapshot(plan.AccountID, live.Labels)
			}
			if readErr != nil {
				step.State = domain.ItemConflict
				step.Code = "live_read_failed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
			target := resolved[step.Name]
			present := slices.Contains(before, target)
			// A target changed since preview is a no-op, never owned by MailPilot.
			if step.Kind == "add" && present || step.Kind == "remove" && !present {
				step.State = domain.ItemSkipped
				step.Code = "membership_changed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
			if plan.Kind == domain.ActionLabelDelta && !slices.Equal(before, row.BeforeLabels) {
				step.State = domain.ItemConflict
				step.Code = "labels_changed"
				plan, err = s.save(plan)
				if err != nil {
					return organizationDTO(plan), err
				}
				continue
			}
		}
		step.State = domain.ItemInFlight
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
		step = &plan.Steps[i]
		if step.Kind == "create_label" {
			label, writeErr := p.CreateLabel(ctx, step.Name)
			if writeErr == nil && (label.AccountID != plan.AccountID || label.Kind != domain.LabelKindUser || label.Name != step.Name || label.ProviderID == "") {
				writeErr = provider.ErrMalformed
			}
			if writeErr != nil {
				step.State = domain.ItemUncertain
				step.Code = "creation_uncertain"
			} else {
				step.State = domain.ItemApplied
				step.ResultID = label.ProviderID
				resolved[step.Name] = label.ProviderID
			}
		} else {
			row := findOrganizationRow(plan, step.EmailID)
			target := resolved[step.Name]
			var writeErr error
			if step.Kind == "add" {
				writeErr = p.ApplyLabel(ctx, row.ProviderID, target)
			} else {
				writeErr = p.RemoveLabel(ctx, row.ProviderID, target)
			}
			if writeErr != nil {
				step.State = domain.ItemUncertain
				step.Code = "write_uncertain"
			} else {
				live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
				if readErr == nil && live.ProviderID != row.ProviderID {
					readErr = provider.ErrMalformed
				}
				var after []domain.ProviderLabelID
				if readErr == nil {
					after, _, readErr = labelsSnapshot(plan.AccountID, live.Labels)
				}
				if readErr != nil {
					step.State = domain.ItemUncertain
					step.Code = "verification_uncertain"
				} else if (step.Kind == "add") != slices.Contains(after, target) {
					step.State = domain.ItemUncertain
					step.Code = "verification_uncertain"
				} else {
					step.State = domain.ItemApplied
					step.ResultID = target
					step.AfterLabels = after
					step.Added = step.Kind == "add"
					if s.refreshLabels(ctx, plan.AccountID, row.EmailID, live.Labels) != nil {
						step.Code = "cache_refresh_failed"
					}
				}
			}
		}
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
		if plan.Steps[i].State == domain.ItemUncertain {
			// Stop dispatching after any uncertain outcome; no blind retry or dependent
			// writes. Remaining work can be reviewed in a fresh batch after reconciliation.
			for j := i + 1; j < len(plan.Steps); j++ {
				plan.Steps[j].State = domain.ItemSkipped
				plan.Steps[j].Code = "blocked_by_uncertainty"
			}
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			break
		}
	}
	return s.finish(plan)
}
func (s *Organization) refreshLabels(ctx context.Context, account domain.AccountID, id domain.EmailID, labels []domain.EmailLabel) error {
	stored, err := s.emails.Get(ctx, account, id)
	if err == nil {
		stored.Email.Labels = labels
		_, err = s.emails.Upsert(ctx, stored.Email)
	}
	return err
}
func (s *Organization) reconcile(ctx context.Context, plan domain.OrganizationPlan, p OrganizationProvider) (OrganizationResponse, error) {
	labels, err := listOrganizationLabels(ctx, plan.AccountID, p)
	if err != nil {
		return organizationDTO(plan), err
	}
	plan.Status = domain.ActionApplying
	plan, err = s.save(plan)
	if err != nil {
		return organizationDTO(plan), err
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.State == domain.ItemPending {
			step.State = domain.ItemSkipped
			step.Code = "recovered_pending"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			continue
		}
		if step.State != domain.ItemInFlight && step.State != domain.ItemUncertain {
			continue
		}
		if step.State == domain.ItemInFlight {
			step.State = domain.ItemUncertain
			step.Code = "interrupted"
			plan, err = s.save(plan)
			if err != nil {
				return organizationDTO(plan), err
			}
			step = &plan.Steps[i]
		}
		if step.Kind == "create_label" {
			if label, ok := labels[step.Name]; ok && label.Kind == domain.LabelKindUser {
				step.State = domain.ItemConflict
				step.Code = "label_exists_ownership_unknown"
			} else {
				step.State = domain.ItemFailed
				step.Code = "label_not_observed"
			}
		}
		if step.Kind != "create_label" {
			row := findOrganizationRow(plan, step.EmailID)
			live, readErr := p.FetchMessageMetadata(ctx, row.ProviderID)
			if readErr != nil {
				return organizationDTO(plan), readErr
			}
			if live.ProviderID != row.ProviderID {
				return organizationDTO(plan), provider.ErrMalformed
			}
			ids, _, readErr := labelsSnapshot(plan.AccountID, live.Labels)
			if readErr != nil {
				return organizationDTO(plan), readErr
			}
			target := step.LabelID
			if target == "" {
				for _, creation := range plan.Steps {
					if creation.Kind == "create_label" && creation.Name == step.Name {
						target = creation.ResultID
					}
				}
			}
			if target == "" {
				return organizationDTO(plan), repository.ErrConflict
			}
			// An ambiguous dispatch cannot establish who added a label. Preserve it and
			// exclude it from undo, even when the desired membership is now observable.
			step.State = domain.ItemConflict
			step.Code = "membership_ownership_unknown"
			if (step.Kind == "add") != slices.Contains(ids, target) {
				step.State = domain.ItemFailed
				step.Code = "change_not_observed"
			}
			if s.refreshLabels(ctx, plan.AccountID, row.EmailID, live.Labels) != nil {
				step.Code = "cache_refresh_failed"
			}
		}
		plan, err = s.save(plan)
		if err != nil {
			return organizationDTO(plan), err
		}
	}
	return s.finish(plan)
}
