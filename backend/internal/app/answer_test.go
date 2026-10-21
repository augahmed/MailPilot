package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/model"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
	"mailpilot.local/core/internal/store/sqlite"
)

func answerFixture(t *testing.T) (*AIService, *sqlite.Store, *summaryFake, map[string]*summaryKeys) {
	t.Helper()
	s, db, f, keys := aiFixture(t)
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		account domain.AccountID
		id      domain.ProviderMessageID
	}{{"a", "selected"}, {"a", "earlier"}, {"a", "unrelated"}, {"b", "foreign"}} {
		if err = chunker.Prepare(context.Background(), row.account, row.id); err != nil {
			t.Fatal(err)
		}
	}
	local := &indexFake{descriptor: model.Identity(), invoke: func(context.Context, []string) ([][]float32, error) { return nil, embedding.ErrUnavailable }}
	s.EnableAnswers(retrieval.New(db.Vectors(), local), db.Indexing())
	return s, db, f, keys
}
func waitAnswer(t *testing.T, s *AIService) AnswerResponse {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r := s.Answer(context.Background(), AnswerRequest{Operation: "status", AccountID: "a"})
		s.mu.Lock()
		busy := s.busy
		s.mu.Unlock()
		if !busy && r.State != "retrieving" && r.State != "running" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("answer job did not finish")
	return AnswerResponse{}
}
func previewAnswer(t *testing.T, s *AIService, question string) AnswerResponse {
	t.Helper()
	r := s.Answer(context.Background(), AnswerRequest{Operation: "retrieve", AccountID: "a", Question: question})
	if r.State != "retrieving" {
		t.Fatal(r.State, r.Code)
	}
	r = waitAnswer(t, s)
	if r.State != "preview" {
		t.Fatal(r.State, r.Code)
	}
	return r
}
func chooseConfirmation(t *testing.T, s *AIService, p AnswerResponse) AnswerResponse {
	t.Helper()
	for _, e := range p.Evidence {
		if e.EmailID == "selected" && strings.Contains(e.Excerpt, "Please confirm") {
			r := s.Answer(context.Background(), AnswerRequest{Operation: "select", AccountID: "a", PreviewID: p.PreviewID, SourceIDs: []string{e.SourceID}})
			if r.Code != "" || r.State != "preview" {
				t.Fatal(r.State, r.Code)
			}
			return r
		}
	}
	t.Fatal("no confirmation evidence")
	return AnswerResponse{}
}
func approveAnswer(s *AIService, p AnswerResponse) AnswerResponse {
	return s.Answer(context.Background(), AnswerRequest{Operation: "approve", AccountID: "a", PreviewID: p.PreviewID, Digest: p.Digest, Approved: true})
}

func TestAnswerExactSelectedEvidenceAndOneUseConsent(t *testing.T) {
	s, _, fake, _ := answerFixture(t)
	p := previewAnswer(t, s, "When is the meeting confirmation due?")
	if fake.answerCount() != 0 || p.Policy != ai.AnswerInstructions() || p.Exhaustive || p.Mode != "keyword" || p.Warning != "semantic_unavailable" || p.Coverage.Semantic != 0 || p.Coverage.Keyword != 3 {
		t.Fatal("unexpected disclosure/coverage", p.Code)
	}
	old := p
	p = chooseConfirmation(t, s, p)
	if old.PreviewID == p.PreviewID || old.Digest == p.Digest || approveAnswer(s, old).Code != "stale_preview" {
		t.Fatal("selection reused approval")
	}
	for _, private := range []string{"EARLIER_THREAD_CANARY", "UNRELATED_MAIL_CANARY", "FOREIGN_ACCOUNT_CANARY", "SIGNATURE_PRIVATE", "HEADER_CANARY"} {
		if strings.Contains(p.Payload, private) {
			t.Fatal("unselected context", private)
		}
	}
	bad := AnswerRequest{Operation: "approve", AccountID: "a", PreviewID: p.PreviewID, Digest: p.Digest}
	if s.Answer(context.Background(), bad).Code != "validation" || fake.answerCount() != 0 {
		t.Fatal("implicit consent")
	}
	bad.Approved = true
	bad.AccountID = "b"
	if s.Answer(context.Background(), bad).Code != "unauthorized" || fake.answerCount() != 0 {
		t.Fatal("wrong-account consent")
	}
	// A wrong-account call clears sensitive state, requiring a fresh preview.
	p = chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
	if r := approveAnswer(s, p); r.State != "running" {
		t.Fatal(r.Code)
	}
	r := waitAnswer(t, s)
	if r.State != "completed" || r.Result == nil || r.Result.Outcome != "answered" || fake.answerCount() != 1 || r.Payload != "" || r.Policy != "" {
		t.Fatal(r.State, r.Code)
	}
	fake.mu.Lock()
	request := fake.answerRequests[0]
	fake.mu.Unlock()
	input, err := request.Input()
	if err != nil || input.Reveal() != p.Payload || len(request.Sources) != 1 || request.Sources[0].EmailID != "selected" {
		t.Fatal("approved bytes changed", err)
	}
	if approveAnswer(s, p).Code != "stale_preview" || fake.answerCount() != 1 {
		t.Fatal("replayed paid request")
	}
	if len(r.Result.Statements[0].Citations) != 1 || len(r.Result.Limitations) == 0 {
		t.Fatal("missing provenance/scope")
	}
}

func TestAnswerInvalidationBeforeDispatchAndDisplay(t *testing.T) {
	for _, kind := range []string{"source", "key", "settings", "session", "expiry", "digest"} {
		t.Run(kind, func(t *testing.T) {
			s, db, fake, keys := answerFixture(t)
			ctx := context.Background()
			session, cancel := context.WithCancel(ctx)
			defer cancel()
			s.session = func() (domain.AccountID, context.Context, error) { return "a", session, nil }
			p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
			switch kind {
			case "source":
				row, _ := db.Emails().Get(ctx, "a", "selected")
				row.Email.BodyText = "Changed confirmation"
				if _, err := db.Emails().Upsert(ctx, row.Email); err != nil {
					t.Fatal(err)
				}
			case "key":
				_ = keys["openai"].Save(ctx, credentials.NewSecret("rotated-fixture-key"))
			case "settings":
				s.Settings(ctx, AISettingsRequest{Operation: "disable", AccountID: "a"})
			case "session":
				cancel()
				session = context.Background()
			case "expiry":
				s.mu.Lock()
				s.now = func() time.Time { return time.UnixMilli(p.ExpiresAt).Add(time.Second) }
				s.mu.Unlock()
			case "digest":
				p.Digest = strings.Repeat("f", 64)
			}
			r := approveAnswer(s, p)
			if r.Code == "" || fake.answerCount() != 0 {
				t.Fatal("invalid context dispatched", r.State, r.Code)
			}
		})
	}
}

func TestAnswerLateResultsAndCancellationAreSuppressed(t *testing.T) {
	for _, kind := range []string{"source", "key", "settings", "session", "cancel", "clear"} {
		t.Run(kind, func(t *testing.T) {
			s, db, fake, keys := answerFixture(t)
			ctx := context.Background()
			session, disconnect := context.WithCancel(ctx)
			defer disconnect()
			s.session = func() (domain.AccountID, context.Context, error) { return "a", session, nil }
			entered, release := make(chan struct{}), make(chan struct{})
			fake.answerInvoke = func(_ context.Context, r ai.AnswerRequest) (ai.AnswerResult, error) {
				close(entered)
				<-release
				return ai.AnswerResult{Outcome: "answered", Statements: []ai.AnswerStatement{{Text: "Late private answer", Citations: []ai.Citation{{SourceID: r.Sources[0].SourceID, Quote: "Please confirm"}}}}, Limitations: []string{}}, nil
			}
			p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
			running := approveAnswer(s, p)
			<-entered
			if r := s.Answer(ctx, AnswerRequest{Operation: "retrieve", AccountID: "a", Question: "Other question"}); r.Code != "busy" {
				t.Fatal("unbounded generation")
			}
			switch kind {
			case "source":
				row, _ := db.Emails().Get(ctx, "a", "selected")
				row.Email.BodyText = "Changed source"
				_, _ = db.Emails().Upsert(ctx, row.Email)
			case "key":
				_ = keys["openai"].Save(ctx, credentials.NewSecret("rotated-key"))
			case "settings":
				s.Settings(ctx, AISettingsRequest{Operation: "disable", AccountID: "a"})
			case "session":
				disconnect()
			case "cancel":
				s.Answer(ctx, AnswerRequest{Operation: "cancel", AccountID: "a", JobID: running.JobID})
			case "clear":
				s.Answer(ctx, AnswerRequest{Operation: "clear", AccountID: "a"})
			}
			close(release)
			r := waitAnswer(t, s)
			if r.Result != nil {
				t.Fatal("late private output exposed", kind)
			}
		})
	}
}

func TestAnswerInventedCitationsAndErrorsAreNotRetried(t *testing.T) {
	for _, kind := range []string{"invented_id", "invented_quote", "uncited", "unrelated_quote", "raw_error", "conflict_one_quote"} {
		t.Run(kind, func(t *testing.T) {
			s, _, f, _ := answerFixture(t)
			f.answerInvoke = func(_ context.Context, r ai.AnswerRequest) (ai.AnswerResult, error) {
				if kind == "raw_error" {
					return ai.AnswerResult{}, errors.New("PRIVATE_KEY_AND_MODEL_OUTPUT")
				}
				result := ai.AnswerResult{Outcome: "answered", Statements: []ai.AnswerStatement{{Text: "Unsupported claim", Citations: []ai.Citation{{SourceID: r.Sources[0].SourceID, Quote: "Please confirm"}}}}, Limitations: []string{}}
				switch kind {
				case "invented_id":
					result.Statements[0].Citations[0].SourceID = "S9"
				case "invented_quote":
					result.Statements[0].Citations[0].Quote = "INVENTED"
				case "uncited":
					result.Statements[0].Citations = nil
				case "unrelated_quote":
					result.Statements[0].Citations[0].Quote = "UNRELATED_MAIL_CANARY"
				case "conflict_one_quote":
					result.Outcome = "conflicting"
				}
				return result, nil
			}
			p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
			approveAnswer(s, p)
			r := waitAnswer(t, s)
			if r.State != "error" || r.Result != nil || f.answerCount() != 1 {
				t.Fatal(r.State, r.Code)
			}
			for i := 0; i < 3; i++ {
				s.Answer(context.Background(), AnswerRequest{Operation: "status", AccountID: "a"})
			}
			if f.answerCount() != 1 || strings.Contains(r.Code, "PRIVATE") || approveAnswer(s, p).Code != "stale_preview" {
				t.Fatal("retry/error leak")
			}
		})
	}
}

type foreignAnswerRetriever struct{ answerRetriever }

func (r foreignAnswerRetriever) SearchMode(ctx context.Context, _ domain.AccountID, q string, k int, f repository.SearchFilter, m string) (retrieval.Result, error) {
	return r.answerRetriever.SearchMode(ctx, "b", q, k, f, m)
}
func TestAnswerWrongAccountRetrievalFailsClosed(t *testing.T) {
	s, _, f, _ := answerFixture(t)
	s.retriever = foreignAnswerRetriever{s.retriever}
	s.Answer(context.Background(), AnswerRequest{Operation: "retrieve", AccountID: "a", Question: "meeting"})
	r := waitAnswer(t, s)
	if r.State != "error" || len(r.Evidence) != 0 || r.Payload != "" || f.answerCount() != 0 {
		t.Fatal("foreign evidence exposed")
	}
}

func TestAnswerIncompleteEvidenceAndAggregateRefusal(t *testing.T) {
	s, db, f, _ := answerFixture(t)
	ctx := context.Background()
	row, _ := db.Emails().Get(ctx, "a", "selected")
	row.Email.BodyState = "unavailable"
	_, _ = db.Emails().Upsert(ctx, row.Email)
	chunker, _ := NewCachedChunker(db.Emails(), db.Chunks())
	_ = chunker.Prepare(ctx, "a", "selected")
	p := previewAnswer(t, s, "meeting")
	partial := false
	for _, e := range p.Evidence {
		if e.EmailID == "selected" {
			partial = e.PartialContent
		}
	}
	if !partial || p.Exhaustive || !strings.Contains(p.Payload, `"exhaustive":false`) {
		t.Fatal("missing partial evidence disclosure")
	}
	p = previewAnswer(t, s, "What is the total of every meeting in my mailbox?")
	if !p.Aggregate || approveAnswer(s, p).Code != "non_exhaustive" || f.answerCount() != 0 {
		t.Fatal("definitive aggregate authorized")
	}
	// An empty filtered retrieval is a local insufficient outcome, never a paid call.
	s.Answer(ctx, AnswerRequest{Operation: "retrieve", AccountID: "a", Question: "meeting", Filter: repository.SearchFilter{Sender: "absent@example.test"}})
	r := waitAnswer(t, s)
	if r.Result == nil || r.Result.Outcome != "insufficient" || f.answerCount() != 0 {
		t.Fatal(r.State, r.Code)
	}
}

func TestAnswerProviderSelectionAndContextSelectionRevokesConsent(t *testing.T) {
	s, _, f, _ := answerFixture(t)
	ctx := context.Background()
	p := previewAnswer(t, s, "meeting")
	r := s.Settings(ctx, AISettingsRequest{Operation: "configure", AccountID: "a", Provider: "second-fixture", Model: "other", APIKey: KeyInput{credentials.NewSecret("synthetic-second")}})
	if r.Code != "" || approveAnswer(s, p).Code != "stale_preview" {
		t.Fatal("settings did not revoke RAG approval")
	}
	p = previewAnswer(t, s, "meeting")
	if p.Provider != "second-fixture" || p.Model != "other" {
		t.Fatal("selected provider ignored")
	}
	approveAnswer(s, p)
	if got := waitAnswer(t, s); got.Result == nil || f.answerCount() != 1 {
		t.Fatal(got.Code)
	}
	s.Summarize(ctx, SummarizeEmailRequest{Operation: "preview", AccountID: "a", EmailID: "selected"})
	if got := s.Answer(ctx, AnswerRequest{Operation: "status", AccountID: "a"}); got.Result != nil {
		t.Fatal("context selection retained result")
	}
}

func TestAnswerDTOBoundsAndNoCapabilityInputs(t *testing.T) {
	raw, err := json.Marshal(AnswerResponse{State: "preview", Payload: strings.Repeat("\x01", 12288)})
	if err != nil || len(raw) < 48*1024 {
		t.Fatal("bad bounds fixture")
	}
	if boundAnswer(AnswerResponse{State: "preview", Payload: strings.Repeat("\x01", 12288)}).Code != "response_too_large" {
		t.Fatal("escaped response exceeded cap")
	}
	for _, r := range []AnswerRequest{{Operation: "retrieve", AccountID: "a", Question: strings.Repeat("é", 1025)}, {Operation: "approve", AccountID: "a", PreviewID: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64)}, {Operation: "select", AccountID: "a", PreviewID: strings.Repeat("a", 64), SourceIDs: []string{"S9"}}, {Operation: "status", Question: "hidden instruction"}} {
		if r.Valid() {
			t.Fatal("unbounded request accepted")
		}
	}
}

func TestAnswerConfirmedDeletionAndCompletedSourceInvalidation(t *testing.T) {
	for _, afterGeneration := range []bool{false, true} {
		t.Run(fmt.Sprint(afterGeneration), func(t *testing.T) {
			s, db, f, _ := answerFixture(t)
			p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
			if afterGeneration {
				approveAnswer(s, p)
				if waitAnswer(t, s).Result == nil {
					t.Fatal("no result")
				}
			}
			ctx := context.Background()
			if err := db.SyncCheckpoints().Commit(ctx, "a", repository.SyncCheckpoint{}, repository.SyncCheckpoint{Phase: "history", Cursor: "synthetic-delete"}, nil, "selected"); err != nil {
				t.Fatal(err)
			}
			var r AnswerResponse
			if afterGeneration {
				r = s.Answer(ctx, AnswerRequest{Operation: "status", AccountID: "a"})
			} else {
				r = approveAnswer(s, p)
			}
			if r.Result != nil || r.Code == "" || (!afterGeneration && f.answerCount() != 0) {
				t.Fatal("deleted evidence used", r.Code)
			}
		})
	}
}
func TestAnswerUnselectedChangesDoNotExpandOrInvalidateApprovedEvidence(t *testing.T) {
	s, db, _, _ := answerFixture(t)
	p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
	row, _ := db.Emails().Get(context.Background(), "a", "unrelated")
	row.Email.BodyText = "Changed unrelated mail"
	_, _ = db.Emails().Upsert(context.Background(), row.Email)
	if r := approveAnswer(s, p); r.State != "running" {
		t.Fatal("unselected message revoked approval", r.Code)
	}
	if r := waitAnswer(t, s); r.Result == nil || len(r.Evidence) != 1 {
		t.Fatal("selected context changed", r.Code)
	}
}
func TestAnswerInsufficientAndConflictingOutcomes(t *testing.T) {
	for _, outcome := range []string{"insufficient", "conflicting"} {
		t.Run(outcome, func(t *testing.T) {
			s, _, f, _ := answerFixture(t)
			f.answerInvoke = func(_ context.Context, r ai.AnswerRequest) (ai.AnswerResult, error) {
				if outcome == "insufficient" {
					return ai.AnswerResult{Outcome: outcome, Statements: []ai.AnswerStatement{}, Limitations: []string{"These selected excerpts do not establish the requested information."}}, nil
				}
				return ai.AnswerResult{Outcome: outcome, Statements: []ai.AnswerStatement{{Text: "The source contains different pieces of evidence requiring review.", Citations: []ai.Citation{{SourceID: r.Sources[0].SourceID, Quote: "Please confirm"}, {SourceID: r.Sources[0].SourceID, Quote: "Tuesday"}}}}, Limitations: []string{"Inspect the source before choosing an interpretation."}}, nil
			}
			p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
			approveAnswer(s, p)
			r := waitAnswer(t, s)
			if r.Result == nil || r.Result.Outcome != outcome {
				t.Fatal(r.State, r.Code)
			}
		})
	}
}

func TestAnswerSelectingAChangedCandidateNeverPublishesAStalePreview(t *testing.T) {
	s, db, _, _ := answerFixture(t)
	p := chooseConfirmation(t, s, previewAnswer(t, s, "When is the meeting confirmation due?"))
	var target string
	for _, source := range p.Evidence {
		if source.EmailID == "unrelated" {
			target = source.SourceID
			break
		}
	}
	if target == "" {
		t.Fatal("missing unselected candidate fixture")
	}
	row, _ := db.Emails().Get(context.Background(), "a", "unrelated")
	row.Email.BodyText = "Changed candidate"
	_, _ = db.Emails().Upsert(context.Background(), row.Email)
	result := s.Answer(context.Background(), AnswerRequest{Operation: "select", AccountID: "a", PreviewID: p.PreviewID, SourceIDs: []string{target}})
	if result.Code != "stale_preview" || len(result.Evidence) != 0 || result.Payload != "" {
		t.Fatal("stale selection published", result.Code)
	}
}
