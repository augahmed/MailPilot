package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/reply"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/store/sqlite"
)

// FakeAIProvider is deliberately network-free. Only synthetic context is used.
type FakeAIProvider struct {
	mu       sync.Mutex
	requests []ai.Request
	block    chan struct{}
	err      error
}

func (f *FakeAIProvider) GenerateDraft(ctx context.Context, r ai.Request) (ai.Result, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	if f.block != nil {
		close(f.block)
		<-ctx.Done()
		return ai.Result{}, ctx.Err()
	}
	return ai.Result{Text: ai.NewText("Synthetic proposed reply.")}, f.err
}
func (f *FakeAIProvider) SummarizeThread(context.Context, ai.Request) (ai.Result, error) {
	return ai.Result{}, errors.New("unexpected summary")
}
func (f *FakeAIProvider) AnswerWithContext(context.Context, ai.Request) (ai.Result, error) {
	return ai.Result{}, errors.New("unexpected answer")
}
func (f *FakeAIProvider) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

type replyKeys struct {
	mu  sync.Mutex
	key credentials.Secret
	err error
}

func (k *replyKeys) Load(context.Context) (credentials.Secret, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return credentials.Secret{}, k.err
	}
	if k.key.Empty() {
		return credentials.Secret{}, credentials.ErrNotFound
	}
	return k.key, nil
}
func (k *replyKeys) Save(_ context.Context, v credentials.Secret) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	k.key = v
	return nil
}
func (k *replyKeys) Delete(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.key = credentials.Secret{}
	return k.err
}

type replyMailbox struct {
	email              domain.Email
	creates, finds     atomic.Int32
	onCreate           func(provider.DraftInput)
	createErr, findErr error
	block              chan struct{}
}

func (f *replyMailbox) Capabilities() provider.Capabilities {
	return provider.Capabilities{CreateDrafts: true}
}
func (f *replyMailbox) FetchMessage(context.Context, domain.ProviderMessageID) (domain.Email, error) {
	return f.email, nil
}
func (f *replyMailbox) CreateDraft(ctx context.Context, d provider.DraftInput) (provider.DraftReference, error) {
	f.creates.Add(1)
	if f.onCreate != nil {
		f.onCreate(d)
	}
	if f.block != nil {
		close(f.block)
		<-ctx.Done()
		return provider.DraftReference{}, ctx.Err()
	}
	if f.createErr != nil {
		return provider.DraftReference{}, f.createErr
	}
	return provider.DraftReference{AccountID: "a", ProviderDraftID: "draft-synthetic", ProviderMessageID: "draft-message"}, nil
}
func (f *replyMailbox) FindDraft(context.Context, provider.DraftInput) (provider.DraftReference, error) {
	f.finds.Add(1)
	if f.findErr != nil {
		return provider.DraftReference{}, f.findErr
	}
	return provider.DraftReference{AccountID: "a", ProviderDraftID: "draft-synthetic", ProviderMessageID: "draft-message"}, nil
}
func replyFixture(t *testing.T) (*ReplyService, *sqlite.Store, *FakeAIProvider, *replyMailbox) {
	t.Helper()
	ctx := context.Background()
	db, e := sqlite.Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []domain.AccountID{"a", "b"} {
		if _, e = db.Accounts().Upsert(ctx, domain.Account{ID: id, Provider: "gmail", ProviderAccountID: string(id), Address: domain.EmailAddress{Address: string(id) + "@example.test"}}); e != nil {
			t.Fatal(e)
		}
	}
	email := domain.Email{AccountID: "a", ID: "selected", ProviderID: "remote-selected", ProviderThreadID: "thread", Sender: domain.EmailAddress{Address: "sender@example.test"}, Subject: "Synthetic meeting", BodyText: "Can you meet on Tuesday?\n-- \nPRIVATE_SIGNATURE\n", BodyState: "complete", ReceivedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), Headers: []domain.EmailHeader{{Name: "Message-ID", Value: "<selected@example.test>"}, {Name: "Reply-To", Value: "Host <host@example.test>"}}}
	if _, e = db.Emails().Upsert(ctx, email); e != nil {
		t.Fatal(e)
	}
	for _, other := range []struct {
		id      string
		account domain.AccountID
		thread  domain.ProviderThreadID
		body    string
	}{{"earlier", "a", "thread", "Earlier relevant discussion."}, {"unrelated", "a", "another-thread", "UNRELATED_MAILBOX_CANARY"}, {"foreign", "b", "thread", "FOREIGN_ACCOUNT_CANARY"}} {
		copy := email
		copy.ID = domain.EmailID(other.id)
		copy.ProviderID = domain.ProviderMessageID(other.id)
		copy.AccountID = other.account
		copy.ProviderThreadID = other.thread
		copy.BodyText = other.body
		copy.ReceivedAt = copy.ReceivedAt.Add(-time.Hour)
		if _, e = db.Emails().Upsert(ctx, copy); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Replies().SaveSettings(ctx, "a", reply.Settings{Model: "synthetic-model", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	fake := &FakeAIProvider{}
	mailbox := &replyMailbox{email: email}
	keys := &replyKeys{key: credentials.NewSecret("synthetic")}
	s := NewReplyService(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, db.Replies(), db.Emails(), func(domain.AccountID) ReplyProvider { return mailbox }, func(domain.AccountID) credentials.SecretStore { return keys }, func(_ domain.AccountID, model string) (ai.AIProvider, ai.Identity, error) {
		return fake, ai.Identity{Provider: "fake", Model: model, External: true}, nil
	})
	t.Cleanup(s.Close)
	return s, db, fake, mailbox
}
func waitReply(t *testing.T, s *ReplyService) ReplyResponse {
	t.Helper()
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		r := s.Handle(context.Background(), ReplyRequest{Operation: "status"})
		if r.State != "generating" && r.State != "creating" && r.State != "reconciling" && r.State != "configuring" {
			s.mu.Lock()
			busy := s.busy
			s.mu.Unlock()
			if !busy {
				return r
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("reply job timed out")
	return ReplyResponse{}
}
func prepareReply(t *testing.T, s *ReplyService, thread bool) ReplyResponse {
	t.Helper()
	r := s.Handle(context.Background(), ReplyRequest{Operation: "prepare", AccountID: "a", EmailID: "selected", Tone: "Friendly", Instruction: "Offer Thursday instead.", IncludeThread: thread})
	if r.Code != "" || r.State != "ai_preview" {
		t.Fatalf("prepare failed: %s/%s", r.State, r.Code)
	}
	return r
}
func generateReply(t *testing.T, s *ReplyService) ReplyResponse {
	t.Helper()
	p := prepareReply(t, s, true)
	r := s.Handle(context.Background(), ReplyRequest{Operation: "generate", AccountID: "a", PreviewID: p.PreviewID, Approved: true})
	if r.State != "generating" {
		t.Fatal("not generating", r.Code)
	}
	r = waitReply(t, s)
	if r.State != "editing" {
		t.Fatal("generation failed", r.Code)
	}
	return r
}
func previewDraft(t *testing.T, s *ReplyService) ReplyResponse {
	t.Helper()
	r := generateReply(t, s)
	p := s.Handle(context.Background(), ReplyRequest{Operation: "preview_draft", AccountID: "a", PreviewID: r.ReviewID, Body: "Human-edited reply."})
	if p.State != "draft_preview" {
		t.Fatal("draft preview failed", p.Code)
	}
	return p
}
func createReply(t *testing.T, s *ReplyService, p ReplyResponse) ReplyResponse {
	t.Helper()
	r := s.Handle(context.Background(), ReplyRequest{Operation: "create", AccountID: "a", ActionID: p.ActionID, PreviewID: p.PreviewID, Approved: true})
	if r.State != "creating" {
		t.Fatal("creation did not start", r.Code)
	}
	return waitReply(t, s)
}
func TestReplyConsentMinimalContextAndReviewedDraft(t *testing.T) {
	s, db, fake, mailbox := replyFixture(t)
	ctx := context.Background()
	one := prepareReply(t, s, false)
	if one.SourceCount != 1 || strings.Contains(one.Context, "Earlier") || fake.count() != 0 {
		t.Fatal("message selection dispatched or expanded")
	}
	p := prepareReply(t, s, true)
	for _, canary := range []string{"UNRELATED_MAILBOX_CANARY", "FOREIGN_ACCOUNT_CANARY", "PRIVATE_SIGNATURE"} {
		if strings.Contains(p.Context, canary) {
			t.Fatal("unnecessary or unauthorized context")
		}
	}
	if p.SourceCount != 2 || !strings.Contains(p.Context, "Earlier relevant") || p.To != "host@example.test" || !strings.Contains(p.Instruction, "Friendly") {
		t.Fatal("incorrect context or routing")
	}
	for _, req := range []ReplyRequest{{Operation: "generate", AccountID: "a", PreviewID: p.PreviewID}, {Operation: "generate", AccountID: "b", PreviewID: p.PreviewID, Approved: true}, {Operation: "generate", AccountID: "a", PreviewID: one.PreviewID, Approved: true}} {
		if r := s.Handle(ctx, req); r.Code == "" {
			t.Fatal("unapproved or stale generation accepted")
		}
	}
	if fake.count() != 0 {
		t.Fatal("AI before approval")
	}
	generated := generateReply(t, s)
	if generated.Body == "" || mailbox.creates.Load() != 0 {
		t.Fatal("generation changed mailbox")
	}
	p = s.Handle(ctx, ReplyRequest{Operation: "preview_draft", AccountID: "a", PreviewID: generated.ReviewID, Body: "Human-edited reply."})
	if p.State != "draft_preview" || mailbox.creates.Load() != 0 {
		t.Fatal("preview wrote remotely")
	}
	stored, e := db.Replies().Get(ctx, "a", p.ActionID)
	if e != nil || stored.Plan.Draft.BodyText != "Human-edited reply." {
		t.Fatal("edited preview not persisted")
	}
	mailbox.onCreate = func(d provider.DraftInput) {
		a, e := db.Replies().Get(ctx, "a", p.ActionID)
		if e != nil || a.State != "in_flight" {
			t.Error("write before journal")
		}
		if d.BodyText != "Human-edited reply." || len(d.Recipients) != 1 || d.Recipients[0].Address != "host@example.test" || d.InReplyTo != "<selected@example.test>" {
			t.Error("edited payload changed")
		}
	}
	if r := s.Handle(ctx, ReplyRequest{Operation: "create", AccountID: "a", ActionID: p.ActionID, PreviewID: p.PreviewID}); r.Code == "" {
		t.Fatal("unconfirmed write")
	}
	r := createReply(t, s, p)
	if r.State != "created" || r.DraftID == "" || mailbox.creates.Load() != 1 {
		t.Fatal("draft not created", r.Code)
	}
	if r = s.Handle(ctx, ReplyRequest{Operation: "create", AccountID: "a", ActionID: p.ActionID, PreviewID: p.PreviewID, Approved: true}); r.Code == "" {
		t.Fatal("duplicate create")
	}
	if r = s.Handle(ctx, ReplyRequest{Operation: "undo_preview", AccountID: "a", ActionID: p.ActionID}); r.Code != "undo_unavailable" || r.UndoAvailable {
		t.Fatal("untruthful undo")
	}
	s.Clear()
	r = waitReply(t, s)
	if r.State != "created" || mailbox.creates.Load() != 1 {
		t.Fatal("lost durable outcome")
	}
}
func TestReplyStaleSourcesAndAccountIsolation(t *testing.T) {
	for _, phase := range []string{"generate", "preview", "create"} {
		t.Run(phase, func(t *testing.T) {
			s, db, fake, mailbox := replyFixture(t)
			ctx := context.Background()
			var p ReplyResponse
			switch phase {
			case "generate":
				p = prepareReply(t, s, true)
			case "preview":
				p = generateReply(t, s)
			case "create":
				p = previewDraft(t, s)
			}
			row, _ := db.Emails().Get(ctx, "a", "selected")
			row.Email.BodyText = "Changed source"
			if _, e := db.Emails().Upsert(ctx, row.Email); e != nil {
				t.Fatal(e)
			}
			var r ReplyResponse
			switch phase {
			case "generate":
				s.Handle(ctx, ReplyRequest{Operation: "generate", AccountID: "a", PreviewID: p.PreviewID, Approved: true})
				r = waitReply(t, s)
				if fake.count() != 0 {
					t.Fatal("stale mail uploaded")
				}
			case "preview":
				r = s.Handle(ctx, ReplyRequest{Operation: "preview_draft", AccountID: "a", PreviewID: p.ReviewID, Body: "Edited"})
			case "create":
				r = createReply(t, s, p)
			}
			if r.Code != "stale_preview" || mailbox.creates.Load() != 0 {
				t.Fatal("stale source accepted", r.Code)
			}
		})
	}
	s, _, fake, _ := replyFixture(t)
	p := prepareReply(t, s, false)
	r := s.Handle(context.Background(), ReplyRequest{Operation: "prepare", AccountID: "a", EmailID: "foreign", Tone: "Formal"})
	if r.Code == "" || fake.count() != 0 {
		t.Fatal("foreign source accepted")
	}
	if got := s.Handle(context.Background(), ReplyRequest{Operation: "status", AccountID: "b"}); got.Code != "unauthorized" || got.Context != "" {
		t.Fatal("account isolation")
	}
	if got := s.Handle(context.Background(), ReplyRequest{Operation: "status"}); got.PreviewID != p.PreviewID {
		t.Fatal("foreign request cleared valid preview")
	}
}
func TestUncertainDraftReconcilesWithoutRetryAndSurvivesRestart(t *testing.T) {
	s, db, _, mailbox := replyFixture(t)
	mailbox.createErr = errors.New("synthetic interrupted request")
	p := previewDraft(t, s)
	if r := createReply(t, s, p); r.State != "uncertain" {
		t.Fatal("uncertainty hidden")
	}
	s.Clear()
	if r := waitReply(t, s); r.State != "uncertain" || r.ActionID != p.ActionID {
		t.Fatal("recovery lost")
	}
	r := s.Handle(context.Background(), ReplyRequest{Operation: "reconcile", AccountID: "a", ActionID: p.ActionID})
	if r.State != "reconciling" {
		t.Fatal(r.Code)
	}
	r = waitReply(t, s)
	if r.State != "created" || mailbox.creates.Load() != 1 || mailbox.finds.Load() != 1 {
		t.Fatal("reconcile retried write", r.Code)
	}
	a, e := db.Replies().Get(context.Background(), "a", p.ActionID)
	if e != nil || a.State != "created" || !a.Reconciled {
		t.Fatal("reconciled outcome not saved")
	}
}
func TestNoMatchRemainsUncertainUntilExplicitManualResolution(t *testing.T) {
	s, _, _, mailbox := replyFixture(t)
	mailbox.createErr = provider.ErrUnavailable
	mailbox.findErr = provider.ErrNotFound
	p := previewDraft(t, s)
	createReply(t, s, p)
	if r := s.Handle(context.Background(), ReplyRequest{Operation: "resolve", AccountID: "a", ActionID: p.ActionID, Approved: true}); r.Code != "reconcile_required" {
		t.Fatal("resolution bypassed reconcile")
	}
	s.Handle(context.Background(), ReplyRequest{Operation: "reconcile", AccountID: "a", ActionID: p.ActionID})
	r := waitReply(t, s)
	if r.State != "uncertain" || !r.Reconciled {
		t.Fatal("no match treated as no creation")
	}
	r = s.Handle(context.Background(), ReplyRequest{Operation: "resolve", AccountID: "a", ActionID: p.ActionID, Approved: true})
	if r.State != "resolved" || mailbox.creates.Load() != 1 {
		t.Fatal("manual resolution wrote remotely")
	}
}
func TestCancelSuppressesGenerationAndJournalsDispatchedDraft(t *testing.T) {
	for _, kind := range []string{"ai", "gmail"} {
		t.Run(kind, func(t *testing.T) {
			s, db, fake, mailbox := replyFixture(t)
			var start ReplyResponse
			var started chan struct{}
			if kind == "ai" {
				fake.block = make(chan struct{})
				started = fake.block
				p := prepareReply(t, s, false)
				start = s.Handle(context.Background(), ReplyRequest{Operation: "generate", AccountID: "a", PreviewID: p.PreviewID, Approved: true})
			} else {
				mailbox.block = make(chan struct{})
				started = mailbox.block
				p := previewDraft(t, s)
				start = s.Handle(context.Background(), ReplyRequest{Operation: "create", AccountID: "a", ActionID: p.ActionID, PreviewID: p.PreviewID, Approved: true})
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("did not start")
			}
			s.Handle(context.Background(), ReplyRequest{Operation: "cancel", AccountID: "a", JobID: start.JobID})
			r := waitReply(t, s)
			if kind == "ai" {
				if r.Body != "" || mailbox.creates.Load() != 0 {
					t.Fatal("cancel leaked/generated mail")
				}
			} else {
				a, e := db.Replies().Latest(context.Background(), "a")
				if e != nil || a.State != "uncertain" || mailbox.creates.Load() != 1 {
					t.Fatal("cancel lost uncertain journal")
				}
			}
		})
	}
}
func TestReplyConfigurationAndDisabledProvider(t *testing.T) {
	s, db, fake, _ := replyFixture(t)
	ctx := context.Background()
	if e := db.Replies().SaveSettings(ctx, "a", reply.Settings{}); e != nil {
		t.Fatal(e)
	}
	if r := s.Handle(ctx, ReplyRequest{Operation: "prepare", AccountID: "a", EmailID: "selected", Tone: "Concise"}); r.Code != "ai_disabled" || fake.count() != 0 {
		t.Fatal("disabled model invoked")
	}
	keys := &replyKeys{}
	s.keys = func(domain.AccountID) credentials.SecretStore { return keys }
	s.Handle(ctx, ReplyRequest{Operation: "configure", AccountID: "a", Model: "test-model", APIKey: KeyInput{credentials.NewSecret("synthetic-key")}})
	r := waitReply(t, s)
	if !r.Configured || r.Model != "test-model" {
		t.Fatal("configuration failed", r.Code)
	}
	saved, e := keys.Load(ctx)
	if e != nil || saved.Reveal() != "synthetic-key" {
		t.Fatal("key not in SecretStore")
	}
	s.Handle(ctx, ReplyRequest{Operation: "disable", AccountID: "a"})
	r = waitReply(t, s)
	if r.Configured {
		t.Fatal("not disabled")
	}
	if _, e = keys.Load(ctx); e != credentials.ErrNotFound {
		t.Fatal("key not removed")
	}
	keys.err = credentials.ErrUnavailable
	s.Handle(ctx, ReplyRequest{Operation: "configure", AccountID: "a", Model: "test-model", APIKey: KeyInput{credentials.NewSecret("synthetic-key")}})
	r = waitReply(t, s)
	if r.Configured || r.Code != "ai_credentials_unavailable" {
		t.Fatal("keyring failure did not fail closed")
	}
}

// Fail the durable dispatch transition: no provider write may follow it.
type blockedReplyJournal struct{ reply.Store }

func (b blockedReplyJournal) Transition(ctx context.Context, a domain.AccountID, id, from, to string, ref provider.DraftReference, code string, reconciled bool) error {
	if to == "in_flight" {
		return repository.ErrStorage
	}
	return b.Store.Transition(ctx, a, id, from, to, ref, code, reconciled)
}
func TestReplyJournalFailurePreventsDispatch(t *testing.T) {
	s, _, _, mailbox := replyFixture(t)
	p := previewDraft(t, s)
	s.store = blockedReplyJournal{s.store}
	r := createReply(t, s, p)
	if r.Code != "operation_failed" || mailbox.creates.Load() != 0 {
		t.Fatal("write without journal")
	}
}
