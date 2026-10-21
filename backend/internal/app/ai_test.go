package app

import (
	"context"
	"encoding/json"
	"errors"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/store/sqlite"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type summaryFake struct {
	mu             sync.Mutex
	requests       []ai.SummaryRequest
	invoke         func(context.Context, ai.SummaryRequest) (ai.SummaryResult, error)
	answerRequests []ai.AnswerRequest
	answerInvoke   func(context.Context, ai.AnswerRequest) (ai.AnswerResult, error)
}

func (f *summaryFake) SummarizeEmail(ctx context.Context, r ai.SummaryRequest) (ai.SummaryResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	if f.invoke != nil {
		return f.invoke(ctx, r)
	}
	return ai.SummaryResult{Summary: ai.NewText("A confirmation is requested."), Requests: []ai.Text{ai.NewText("Please confirm")}, Deadlines: []ai.Text{ai.NewText("Tuesday")}}, nil
}
func (f *summaryFake) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

type summaryKeys struct {
	mu    sync.Mutex
	key   credentials.Secret
	err   error
	loads int
}

func (k *summaryKeys) Load(context.Context) (credentials.Secret, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.loads++
	if k.err != nil {
		return credentials.Secret{}, k.err
	}
	if k.key.Empty() {
		return credentials.Secret{}, credentials.ErrNotFound
	}
	return k.key, nil
}
func (k *summaryKeys) Save(_ context.Context, key credentials.Secret) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	k.key = key
	return nil
}
func (k *summaryKeys) Delete(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	k.key = credentials.Secret{}
	return nil
}
func aiFixture(t *testing.T) (*AIService, *sqlite.Store, *summaryFake, map[string]*summaryKeys) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []domain.AccountID{"a", "b"} {
		if _, err = db.Accounts().Upsert(ctx, domain.Account{ID: id, Provider: "gmail", ProviderAccountID: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		id      string
		account domain.AccountID
		body    string
	}{{"selected", "a", "Please confirm by Tuesday.\n--\nSIGNATURE_PRIVATE"}, {"earlier", "a", "EARLIER_THREAD_CANARY"}, {"unrelated", "a", "UNRELATED_MAIL_CANARY"}, {"foreign", "b", "FOREIGN_ACCOUNT_CANARY"}} {
		e := domain.Email{ID: domain.EmailID(v.id), AccountID: v.account, ProviderID: domain.ProviderMessageID(v.id), ProviderThreadID: "same-thread", Subject: "Synthetic meeting", Sender: domain.EmailAddress{Address: "sender@example.test"}, BodyText: v.body, BodyState: "complete", ReceivedAt: time.Now(), Headers: []domain.EmailHeader{{Name: "X-Private", Value: "HEADER_CANARY"}}}
		if _, err = db.Emails().Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	fake := &summaryFake{}
	keys := map[string]*summaryKeys{"openai": {}, "second-fixture": {}}
	adapters := map[string]ProviderAdapter{}
	for id, key := range keys {
		adapters[id] = ProviderAdapter{External: true, Keys: func(domain.AccountID) credentials.SecretStore { return key }, New: func(_ domain.AccountID, model string) (ai.AIProvider, error) {
			if model == "bad" {
				return nil, ai.ErrInvalid
			}
			return fake, nil
		}}
	}
	s := NewAIService(ctx, func() (domain.AccountID, context.Context, error) { return "a", ctx, nil }, db.Emails(), db.AISettings(), adapters)
	t.Cleanup(s.Close)
	if r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "openai", Model: "fixture", APIKey: KeyInput{credentials.NewSecret("synthetic-openai")}}); r.Code != "" {
		t.Fatal(r.Code)
	}
	return s, db, fake, keys
}
func previewSummary(t *testing.T, s *AIService) SummarizeEmailResponse {
	t.Helper()
	r := s.Summarize(context.Background(), SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "selected"})
	if r.State != "preview" || r.Code != "" {
		t.Fatal(r.State, r.Code)
	}
	return r
}
func approveSummary(t *testing.T, s *AIService, p SummarizeEmailResponse) SummarizeEmailResponse {
	t.Helper()
	return s.Summarize(context.Background(), SummarizeEmailRequest{Operation: "approve", AccountID: "a", EmailID: "selected", PreviewID: p.PreviewID, Approved: true})
}
func waitSummary(t *testing.T, s *AIService) SummarizeEmailResponse {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		r := s.Summarize(context.Background(), SummarizeEmailRequest{Operation: "status", AccountID: "a", EmailID: "selected"})
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if !busy && r.State != "running" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("summary timed out")
	return SummarizeEmailResponse{}
}
func TestSummaryOnlySelectedCachedEmailAndSingleUseConsent(t *testing.T) {
	s, _, fake, _ := aiFixture(t)
	p := previewSummary(t, s)
	if fake.count() != 0 || p.Provider != "openai" || p.Model != "fixture" || p.Policy != ai.Instructions() {
		t.Fatal("undisclosed dispatch")
	}
	for _, canary := range []string{"EARLIER_THREAD_CANARY", "UNRELATED_MAIL_CANARY", "FOREIGN_ACCOUNT_CANARY", "SIGNATURE_PRIVATE", "HEADER_CANARY"} {
		if strings.Contains(p.Email, canary) {
			t.Fatal("unrelated context")
		}
	}
	for _, req := range []SummarizeEmailRequest{{Operation: "approve", AccountID: "a", EmailID: "selected", PreviewID: p.PreviewID}, {Operation: "approve", AccountID: "b", EmailID: "selected", PreviewID: p.PreviewID, Approved: true}, {Operation: "approve", AccountID: "a", EmailID: "foreign", PreviewID: p.PreviewID, Approved: true}} {
		if r := s.Summarize(context.Background(), req); r.Code == "" {
			t.Fatal("unapproved call")
		}
	}
	if fake.count() != 0 {
		t.Fatal("AI called before approval")
	}
	if r := approveSummary(t, s, p); r.State != "running" {
		t.Fatal(r.Code)
	}
	r := waitSummary(t, s)
	if r.State != "completed" || r.Result == nil || len(r.Result.Requests) != 1 || len(r.Result.Deadlines) != 1 || r.Email != "" || r.Policy != "" || fake.count() != 1 {
		t.Fatal("invalid normalized result", r.State, r.Code)
	}
	fake.mu.Lock()
	got := fake.requests[0].Email.Reveal()
	fake.mu.Unlock()
	if got != p.Email {
		t.Fatal("approved payload changed")
	}
	if r := approveSummary(t, s, p); r.Code != "stale_preview" {
		t.Fatal("replay")
	}
}
func TestProvidersKeepSeparateKeysAndSwitchingRevokesApproval(t *testing.T) {
	s, _, fake, keys := aiFixture(t)
	p := previewSummary(t, s)
	ctx := context.Background()
	r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "second-fixture", Model: "other", APIKey: KeyInput{credentials.NewSecret("synthetic-second")}})
	if r.Code != "" || r.Provider != "second-fixture" || len(r.Providers) != 2 || keys["openai"].key.Reveal() != "synthetic-openai" || keys["second-fixture"].key.Reveal() != "synthetic-second" {
		t.Fatal("provider isolation")
	}
	if r := approveSummary(t, s, p); r.Code != "stale_preview" || fake.count() != 0 {
		t.Fatal("old provider approval reused")
	}
	p = previewSummary(t, s)
	if p.Provider != "second-fixture" {
		t.Fatal("user choice ignored")
	}
	approveSummary(t, s, p)
	if r := waitSummary(t, s); r.Result == nil {
		t.Fatal(r.Code)
	}
	r = s.Settings(ctx, AISettingsRequest{Operation: "disable", AccountID: "a"})
	if r.Code != "" || r.Enabled || !keys["second-fixture"].key.Empty() || keys["openai"].key.Empty() {
		t.Fatal("disabled wrong provider")
	}
	if r := s.Summarize(ctx, SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "selected"}); r.Code != "ai_disabled" {
		t.Fatal(r.Code)
	}
	r = s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "openai", Model: "fixture"})
	if r.Code != "" || !r.Enabled {
		t.Fatal("saved provider key unavailable", r.Code)
	}
}
func TestSummaryRejectsChangedOrForeignSourcesAndMissingContent(t *testing.T) {
	s, db, fake, _ := aiFixture(t)
	p := previewSummary(t, s)
	ctx := context.Background()
	row, err := db.Emails().Get(ctx, "a", "selected")
	if err != nil {
		t.Fatal(err)
	}
	row.Email.BodyText = "Changed content"
	if _, err = db.Emails().Upsert(ctx, row.Email); err != nil {
		t.Fatal(err)
	}
	if r := approveSummary(t, s, p); r.Code != "stale_preview" || fake.count() != 0 {
		t.Fatal("stale payload dispatched")
	}
	if r := s.Summarize(ctx, SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "foreign"}); r.Code == "" {
		t.Fatal("foreign content")
	}
	row.Email.BodyState = "unavailable"
	if _, err = db.Emails().Upsert(ctx, row.Email); err != nil {
		t.Fatal(err)
	}
	if r := s.Summarize(ctx, SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "selected"}); r.Code != "context_unavailable" {
		t.Fatal("incomplete mail accepted")
	}
}
func TestSettingsFailClosedAndDoNotReturnKeys(t *testing.T) {
	s, db, fake, keys := aiFixture(t)
	keys["openai"].err = credentials.ErrUnavailable
	ctx := context.Background()
	if r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "openai", Model: "fixture"}); r.Code != "ai_credentials_unavailable" {
		t.Fatal(r.Code)
	}
	if r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "missing", Model: "fixture"}); r.Code != "unsupported" {
		t.Fatal("unknown adapter accepted")
	}
	if r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "b", Provider: "openai", Model: "fixture"}); r.Code != "unauthorized" {
		t.Fatal("foreign settings")
	}
	if cfg, err := db.AISettings().Settings(ctx, "b"); err != nil || cfg.Enabled {
		t.Fatal("cross-account preference")
	}
	if fake.count() != 0 {
		t.Fatal("settings uploaded email")
	}
}
func TestSummaryCancellationAndSettingsChangeSuppressLateResults(t *testing.T) {
	for _, kind := range []string{"cancel", "settings", "session", "source"} {
		t.Run(kind, func(t *testing.T) {
			s, db, fake, _ := aiFixture(t)
			started := make(chan struct{})
			release := make(chan struct{})
			fake.invoke = func(context.Context, ai.SummaryRequest) (ai.SummaryResult, error) {
				close(started)
				<-release
				return ai.SummaryResult{Summary: ai.NewText("Late private result"), Requests: []ai.Text{}, Deadlines: []ai.Text{}}, nil
			}
			session, disconnect := context.WithCancel(context.Background())
			defer disconnect()
			s.session = func() (domain.AccountID, context.Context, error) { return "a", session, nil }
			p := previewSummary(t, s)
			running := approveSummary(t, s, p)
			<-started
			if r := s.Summarize(context.Background(), SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "earlier"}); r.Code != "busy" {
				t.Fatal("unbounded AI concurrency")
			}
			switch kind {
			case "cancel":
				s.Summarize(context.Background(), SummarizeEmailRequest{Operation: "cancel", AccountID: "a", EmailID: "selected", JobID: running.JobID})
			case "settings":
				s.Settings(context.Background(), AISettingsRequest{Operation: "disable", AccountID: "a"})
			case "session":
				disconnect()
			case "source":
				row, _ := db.Emails().Get(context.Background(), "a", "selected")
				row.Email.BodyText = "New content"
				if _, err := db.Emails().Upsert(context.Background(), row.Email); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			r := waitSummary(t, s)
			if r.Result != nil {
				t.Fatal("late private result exposed")
			}
		})
	}
}
func TestSummaryRejectsUnsupportedModelOutput(t *testing.T) {
	s, _, fake, _ := aiFixture(t)
	fake.invoke = func(context.Context, ai.SummaryRequest) (ai.SummaryResult, error) {
		return ai.SummaryResult{Summary: ai.NewText("text"), Requests: []ai.Text{ai.NewText("INVENTED")}, Deadlines: []ai.Text{}}, nil
	}
	p := previewSummary(t, s)
	approveSummary(t, s, p)
	if r := waitSummary(t, s); r.Result != nil || r.State != "error" {
		t.Fatal("unsupported extraction exposed")
	}
	fake.invoke = func(context.Context, ai.SummaryRequest) (ai.SummaryResult, error) {
		return ai.SummaryResult{}, errors.New("private upstream")
	}
	p = previewSummary(t, s)
	approveSummary(t, s, p)
	if r := waitSummary(t, s); r.Code != "operation_failed" {
		t.Fatal("raw error leaked")
	}
}

func TestSummaryPreviewBoundsUnicodeAndEscapedResponses(t *testing.T) {
	s, db, fake, _ := aiFixture(t)
	ctx := context.Background()
	row, err := db.Emails().Get(ctx, "a", "selected")
	if err != nil {
		t.Fatal(err)
	}
	row.Email.BodyText = strings.Repeat("猫", 6000)
	if _, err = db.Emails().Upsert(ctx, row.Email); err != nil {
		t.Fatal(err)
	}
	p := previewSummary(t, s)
	if !p.Truncated || len(p.Email) > ai.MaxInputBytes || !utf8.ValidString(p.Email) || fake.count() != 0 {
		t.Fatal("unbounded or undisclosed preview")
	}
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) >= 48*1024 {
		t.Fatal("preview frame overflow", err)
	}
	result := &SummaryResultDTO{Summary: strings.Repeat("\x01", 4000), Requests: []string{}, Deadlines: []string{}}
	for i := 0; i < 10; i++ {
		result.Requests = append(result.Requests, strings.Repeat("\x01", 500))
		result.Deadlines = append(result.Deadlines, strings.Repeat("\x01", 500))
	}
	r := boundSummary(SummarizeEmailResponse{State: "completed", Result: result})
	if r.Code != "response_too_large" || r.Result != nil {
		t.Fatal("escaped output overflow crossed IPC")
	}
}

func (f *summaryFake) AnswerWithEvidence(ctx context.Context, r ai.AnswerRequest) (ai.AnswerResult, error) {
	f.mu.Lock()
	f.answerRequests = append(f.answerRequests, r)
	invoke := f.answerInvoke
	f.mu.Unlock()
	if invoke != nil {
		return invoke(ctx, r)
	}
	return ai.AnswerResult{Outcome: "answered", Statements: []ai.AnswerStatement{{Text: "The selected source requests confirmation.", Citations: []ai.Citation{{SourceID: r.Sources[0].SourceID, Quote: clipSummary(r.Sources[0].Excerpt, 300)}}}}, Limitations: []string{}}, nil
}
func (f *summaryFake) answerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.answerRequests)
}
