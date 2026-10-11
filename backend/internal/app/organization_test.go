package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/repository"
)

type organizationFake struct {
	mu          sync.Mutex
	email       domain.Email
	labels      []domain.EmailLabel
	writes      []string
	fail        string
	afterWrite  func()
	beforeWrite func(string)
}

func (f *organizationFake) Capabilities() provider.Capabilities {
	return provider.Capabilities{CreateLabels: true, ModifyLabels: true}
}
func (f *organizationFake) ListLabels(ctx context.Context, p provider.PageOptions) (provider.LabelPage, error) {
	if ctx.Err() != nil {
		return provider.LabelPage{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return provider.LabelPage{Labels: slices.Clone(f.labels)}, nil
}
func (f *organizationFake) FetchMessage(ctx context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	if ctx.Err() != nil {
		return domain.Email{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.email
	e.Labels = slices.Clone(e.Labels)
	return e, nil
}
func (f *organizationFake) FetchMessageMetadata(ctx context.Context, id domain.ProviderMessageID) (provider.MessageMetadata, error) {
	e, err := f.FetchMessage(ctx, id)
	return provider.MessageMetadata{ProviderID: e.ProviderID, Labels: e.Labels}, err
}
func (f *organizationFake) CreateLabel(ctx context.Context, name string) (domain.EmailLabel, error) {
	if ctx.Err() != nil {
		return domain.EmailLabel{}, ctx.Err()
	}
	if f.beforeWrite != nil {
		f.beforeWrite("create_label")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "create")
	label := domain.EmailLabel{AccountID: "a", ProviderID: "created", Name: name, Kind: domain.LabelKindUser}
	f.labels = append(f.labels, label)
	if f.fail == "create" {
		return domain.EmailLabel{}, provider.ErrUnavailable
	}
	return label, nil
}
func (f *organizationFake) ApplyLabel(ctx context.Context, m domain.ProviderMessageID, id domain.ProviderLabelID) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.beforeWrite != nil {
		f.beforeWrite("add")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "add")
	for _, l := range f.labels {
		if l.ProviderID == id {
			f.email.Labels = append(f.email.Labels, l)
		}
	}
	if f.afterWrite != nil {
		f.afterWrite()
	}
	if f.fail == "add" {
		return provider.ErrUnavailable
	}
	return nil
}
func (f *organizationFake) RemoveLabel(ctx context.Context, m domain.ProviderMessageID, id domain.ProviderLabelID) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.beforeWrite != nil {
		f.beforeWrite("remove")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "remove")
	f.email.Labels = slices.DeleteFunc(f.email.Labels, func(l domain.EmailLabel) bool { return l.ProviderID == id })
	return nil
}
func organizationFixture(t *testing.T) (*Organization, *organizationFake, domain.Email) {
	t.Helper()
	store, review, email, _ := reviewFixture(t)
	ctx := context.Background()
	email.Labels = []domain.EmailLabel{{AccountID: "a", ProviderID: "INBOX", Name: "INBOX", Kind: domain.LabelKindSystem}, {AccountID: "a", ProviderID: "keep", Name: "Keep", Kind: domain.LabelKindUser}}
	if _, err := store.Emails().Upsert(ctx, email); err != nil {
		t.Fatal(err)
	}
	result := review.Review(ctx, ClassificationRequest{Operation: "run", AccountID: "a"})
	if result.Errors != 0 {
		t.Fatal(result)
	}
	fake := &organizationFake{email: email, labels: slices.Clone(email.Labels)}
	service := NewOrganization(ctx, review.session, func(domain.AccountID) OrganizationProvider { return fake }, store.Emails(), store.Categories(), store.Classifications(), store.Organization(), review.engine)
	t.Cleanup(service.Close)
	return service, fake, email
}
func awaitOrganization(t *testing.T, s *Organization, p OrganizationRequest) OrganizationResponse {
	t.Helper()
	r := s.Handle(context.Background(), p)
	deadline := time.Now().Add(3 * time.Second)
	for r.State == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		r = s.Handle(context.Background(), OrganizationRequest{Operation: "status", AccountID: "a"})
	}
	if r.State == "running" {
		t.Fatal("worker did not finish")
	}
	return r
}
func previewOrganization(t *testing.T, s *Organization, email domain.Email) OrganizationResponse {
	t.Helper()
	r := awaitOrganization(t, s, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if r.Code != "" || r.State != "preview" {
		t.Fatalf("preview failed: %+v", r)
	}
	return r
}
func applyOrganization(t *testing.T, s *Organization, preview OrganizationResponse) OrganizationResponse {
	return awaitOrganization(t, s, OrganizationRequest{Operation: "apply", AccountID: "a", ActionID: preview.ID, Digest: preview.Digest})
}
func TestOrganizationDryRunSelectionAndPersistence(t *testing.T) {
	s, f, email := organizationFixture(t)
	ctx := context.Background()
	r := awaitOrganization(t, s, OrganizationRequest{Operation: "dry_run", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if r.State != "dry_run" || !r.DryRun || r.ID != "" || len(r.Rows) != 1 || r.Rows[0].ProposedLabel != "MailPilot/Receipts" || r.Rows[0].Confidence != .6 || len(r.Steps) != 2 || r.Steps[0].Kind != "create_label" || len(f.writes) != 0 {
		t.Fatalf("wrong dry run: %+v", r)
	}
	if _, err := s.journal.Latest(ctx, "a", false); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("dry run wrote journal", err)
	}
	preview := previewOrganization(t, s, email)
	saved, err := s.journal.Get(ctx, "a", domain.ActionID(preview.ID))
	if err != nil || saved.Digest != preview.Digest || len(saved.Steps) != 2 || len(f.writes) != 0 {
		t.Fatal("preview persistence", err)
	}
	if _, err := s.journal.Get(ctx, "other", saved.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("journal isolation")
	}
	// Observe durable in-flight evidence before every remote dispatch.
	f.beforeWrite = func(kind string) {
		p, err := s.journal.Get(ctx, "a", saved.ID)
		if err != nil || p.Status != domain.ActionApplying {
			t.Error("write not journaled", err)
		}
		found := false
		for _, step := range p.Steps {
			if step.Kind == kind && step.State == domain.ItemInFlight {
				found = true
			}
		}
		if !found {
			t.Error("dispatch without in-flight evidence")
		}
	}
	applied := applyOrganization(t, s, preview)
	if applied.State != "applied" || applied.Code != "" || len(f.writes) != 2 {
		t.Fatalf("apply failed: %+v", applied)
	}
	persisted, err := s.journal.Get(ctx, "a", saved.ID)
	if err != nil || !persisted.Steps[1].Added || persisted.Steps[1].ResultID != "created" {
		t.Fatal("addition not persisted", err)
	}
	replay := applyOrganization(t, s, preview)
	if replay.Code != "stale_preview" || len(f.writes) != 2 {
		t.Fatal("confirmed batch replayed")
	}
}
func TestOrganizationUndoPreservesPreexistingAndUnrelatedLabels(t *testing.T) {
	s, f, email := organizationFixture(t)
	preview := previewOrganization(t, s, email)
	applied := applyOrganization(t, s, preview)
	if applied.State != "applied" {
		t.Fatal(applied)
	}
	// Another client adds a label after application. Undo targets only the actual
	// MailPilot addition and allows unrelated live changes even after undo preview.
	extra := domain.EmailLabel{AccountID: "a", ProviderID: "external", Name: "External", Kind: domain.LabelKindUser}
	f.email.Labels = append(f.email.Labels, extra)
	f.labels = append(f.labels, extra)
	undo := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.State != "preview" || undo.Kind != "undo" || len(undo.Steps) != 1 || undo.Steps[0].Kind != "remove" {
		t.Fatalf("wrong undo preview: %+v", undo)
	}
	later := domain.EmailLabel{AccountID: "a", ProviderID: "later", Name: "Later", Kind: domain.LabelKindUser}
	f.email.Labels = append(f.email.Labels, later)
	f.labels = append(f.labels, later)
	done := applyOrganization(t, s, undo)
	if done.State != "applied" || len(f.email.Labels) != 4 {
		t.Fatalf("undo failed: %+v", done)
	}
	for _, id := range []domain.ProviderLabelID{"INBOX", "keep", "external", "later"} {
		if !slices.ContainsFunc(f.email.Labels, func(l domain.EmailLabel) bool { return l.ProviderID == id }) {
			t.Fatal("unrelated label removed")
		}
	}
	if !slices.ContainsFunc(f.labels, func(l domain.EmailLabel) bool { return l.ProviderID == "created" }) {
		t.Fatal("Gmail label was deleted")
	}
	again := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if again.Code != "stale_preview" {
		t.Fatal("allowed duplicate undo", again)
	}
}
func TestOrganizationExistingMembershipIsNeverOwned(t *testing.T) {
	s, f, email := organizationFixture(t)
	label := domain.EmailLabel{AccountID: "a", ProviderID: "already", Name: "MailPilot/Receipts", Kind: domain.LabelKindUser}
	email.Labels = append(email.Labels, label)
	f.email = email
	f.labels = append(f.labels, label)
	if _, err := s.emails.Upsert(context.Background(), email); err != nil {
		t.Fatal(err)
	}
	cats, _ := s.categories.List(context.Background(), "a")
	run, _ := s.engine.Classify(context.Background(), email, cats)
	if err := s.results.Replace(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	preview := previewOrganization(t, s, email)
	if preview.Rows[0].Change != "none" || len(preview.Steps) != 0 {
		t.Fatal("planned preexisting membership")
	}
	applied := applyOrganization(t, s, preview)
	if applied.State != "applied" || len(f.writes) != 0 {
		t.Fatal("no-op wrote mailbox")
	}
	undo := awaitOrganization(t, s, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.Code != "not_found" {
		t.Fatal("preexisting membership offered for undo", undo)
	}
}
func TestOrganizationStalePredictionsAndUnsafeRequests(t *testing.T) {
	s, f, email := organizationFixture(t)
	preview := previewOrganization(t, s, email)
	for _, p := range []OrganizationRequest{{Operation: "apply", AccountID: "other", ActionID: preview.ID, Digest: preview.Digest}, {Operation: "apply", AccountID: "a", ActionID: preview.ID, Digest: "bad"}, {Operation: "preview", AccountID: "a", EmailIDs: []string{string(email.ID), string(email.ID)}}, {Operation: "delete", AccountID: "a"}} {
		r := s.Handle(context.Background(), p)
		if r.Code != "unauthorized" && r.Code != "validation" {
			t.Fatal("unsafe request accepted")
		}
	}
	f.email.Subject = "Changed remotely"
	r := applyOrganization(t, s, preview)
	if r.Code != "stale_preview" || len(f.writes) != 0 {
		t.Fatal("stale preview applied", r)
	}
}
func TestOrganizationUncertainDispatchReconcilesWithoutRetryOrUnsafeUndo(t *testing.T) {
	s, f, email := organizationFixture(t)
	f.fail = "add"
	preview := previewOrganization(t, s, email)
	r := applyOrganization(t, s, preview)
	if r.State != "uncertain" || len(f.writes) != 2 {
		t.Fatal("uncertain write not preserved", r)
	}
	blocked := awaitOrganization(t, s, OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if blocked.Code != "reconciliation_required" {
		t.Fatal("write bypassed recovery")
	}
	// A fresh instance has only persisted evidence, as after a desktop restart.
	restarted := NewOrganization(context.Background(), s.session, s.factory, s.emails, s.categories, s.results, s.journal, s.engine)
	defer restarted.Close()
	recovered := awaitOrganization(t, restarted, OrganizationRequest{Operation: "reconcile", AccountID: "a"})
	if recovered.State != "partial" || len(f.writes) != 2 {
		t.Fatal("reconciliation retried write", recovered)
	}
	undo := awaitOrganization(t, restarted, OrganizationRequest{Operation: "undo_preview", AccountID: "a"})
	if undo.Code != "not_found" {
		t.Fatal("ambiguous addition offered for undo")
	}
	if !slices.ContainsFunc(f.email.Labels, func(l domain.EmailLabel) bool { return l.ProviderID == "created" }) {
		t.Fatal("reconcile removed ambiguous membership")
	}
}
func TestOrganizationCancellationAfterDispatchAndCategoryEdits(t *testing.T) {
	s, f, email := organizationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.session = func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }
	preview := previewOrganization(t, s, email)
	f.afterWrite = func() { cancel(); s.mu.Lock(); stop := s.cancel; s.mu.Unlock(); stop() }
	r := applyOrganization(t, s, preview)
	if r.Code != "unauthorized" {
		t.Fatal("session cancellation leaked results")
	}
	s.Close()
	saved, err := s.journal.Get(context.Background(), "a", domain.ActionID(preview.ID))
	if err != nil || saved.Status != domain.ActionUncertain {
		t.Fatal("cancellation lost dispatch evidence", err)
	}
}

func TestOrganizationWaitingForSyncIsCancellable(t *testing.T) {
	s, f, email := organizationFixture(t)
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	s.SetOperationGate(gate)
	r := s.Handle(context.Background(), OrganizationRequest{Operation: "preview", AccountID: "a", EmailIDs: []string{string(email.ID)}})
	if r.State != "running" {
		t.Fatal("job did not start")
	}
	s.Handle(context.Background(), OrganizationRequest{Operation: "cancel", AccountID: "a"})
	deadline := time.Now().Add(time.Second)
	for s.Busy() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Busy() {
		<-gate
		t.Fatal("cancel waited for sync to finish")
	}
	<-gate
	if len(f.writes) != 0 {
		t.Fatal("cancel dispatched a write")
	}
}

func TestOrganizationUncertainCreationIsReusedOnlyAfterExplicitReview(t *testing.T) {
	s, f, email := organizationFixture(t)
	f.fail = "create"
	preview := previewOrganization(t, s, email)
	r := applyOrganization(t, s, preview)
	if r.State != "uncertain" || len(f.writes) != 1 {
		t.Fatal("uncertain creation dispatched dependent writes", r)
	}
	r = awaitOrganization(t, s, OrganizationRequest{Operation: "reconcile", AccountID: "a"})
	if r.State != "failed" || len(f.writes) != 1 {
		t.Fatal("creation reconciliation retried write", r)
	}
	f.fail = ""
	fresh := previewOrganization(t, s, email)
	if len(fresh.Steps) != 1 || fresh.Steps[0].Kind != "add" {
		t.Fatal("fresh preview did not reuse observed label")
	}
	r = applyOrganization(t, s, fresh)
	if r.State != "applied" || len(f.writes) != 2 {
		t.Fatal("fresh confirmed addition failed", r)
	}
}
func TestOrganizationResponsesBoundJSONEscaping(t *testing.T) {
	r := emptyOrganization()
	r.AccountID = "a"
	r.Rows = []OrganizationRowDTO{{EmailID: "email", Subject: string(make([]byte, 20000)), CurrentLabels: []string{}}}
	bounded := boundedOrganization(r)
	if bounded.Code != "validation" || len(bounded.Rows) != 0 {
		t.Fatal("oversized escaped response reached bridge")
	}
}
