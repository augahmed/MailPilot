package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mailpilot.local/core/internal/domain"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// FakeAIProvider never uses a network or real credential. It exercises the same
// contract as every real adapter; synthetic fixtures are the only inputs.
type FakeAIProvider struct {
	calls  atomic.Int32
	invoke func(context.Context, Operation, Request) (Result, error)
}

func (f *FakeAIProvider) call(ctx context.Context, op Operation, r Request) (Result, error) {
	f.calls.Add(1)
	if f.invoke != nil {
		return f.invoke(ctx, op, r)
	}
	return Result{NewText("Synthetic " + string(op))}, nil
}
func (f *FakeAIProvider) GenerateDraft(c context.Context, r Request) (Result, error) {
	return f.call(c, Draft, r)
}
func (f *FakeAIProvider) SummarizeThread(c context.Context, r Request) (Result, error) {
	return f.call(c, Summary, r)
}
func (f *FakeAIProvider) AnswerWithContext(c context.Context, r Request) (Result, error) {
	return f.call(c, Answer, r)
}

var _ AIProvider = (*FakeAIProvider)(nil)

func sampleRequest() Request {
	return Request{NewText("Please help with this synthetic email"), NewText("Synthetic meeting on Tuesday"), 500}
}
func newTestService(t *testing.T, f AIProvider) *Service {
	t.Helper()
	s, e := NewService(f, Identity{"fake", "fixture-v1", false})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestDisabledService(t *testing.T) {
	for _, s := range []*Service{nil, {}} {
		if s.Enabled() {
			t.Fatal("enabled without provider")
		}
		if _, e := s.Prepare(context.Background(), "a", Draft, sampleRequest()); e != ErrDisabled {
			t.Fatal(e)
		}
		if _, e := s.Execute(context.Background(), "a", "id", true); e != ErrDisabled {
			t.Fatal(e)
		}
		s.Clear()
	}
	s, e := NewService(nil, Identity{})
	if e != nil || s.Enabled() {
		t.Fatal("no provider must be supported")
	}
}
func TestOperationsRequireExactSingleUseApproval(t *testing.T) {
	for _, op := range []Operation{Draft, Summary, Answer} {
		t.Run(string(op), func(t *testing.T) {
			f := &FakeAIProvider{invoke: func(_ context.Context, got Operation, r Request) (Result, error) {
				if got != op || r.Context.Reveal() != sampleRequest().Context.Reveal() {
					t.Fatal("preview mutated or incorrect operation")
				}
				return Result{NewText("synthetic result")}, nil
			}}
			s := newTestService(t, f)
			ctx := context.Background()
			p, e := s.Prepare(ctx, "a", op, sampleRequest())
			if e != nil {
				t.Fatal(e)
			}
			if f.calls.Load() != 0 || p.Identity.Provider != "fake" || p.Instructions.Reveal() == "" {
				t.Fatal("preview dispatch or missing disclosure")
			}
			// Editing the returned preview cannot change the privately retained request.
			p.Request.Context = NewText("tampered")
			for _, a := range []struct {
				account, id string
				ok          bool
			}{{"b", p.ID, true}, {"a", "wrong", true}, {"a", p.ID, false}} {
				if _, e = s.Execute(ctx, domain.AccountID(a.account), a.id, a.ok); e != ErrConsent {
					t.Fatal(e)
				}
			}
			if f.calls.Load() != 0 {
				t.Fatal("unapproved dispatch")
			}
			r, e := s.Execute(ctx, "a", p.ID, true)
			if e != nil || r.Text.Reveal() != "synthetic result" {
				t.Fatal(e)
			}
			if _, e = s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
				t.Fatal("replay allowed")
			}
			if f.calls.Load() != 1 {
				t.Fatal("unexpected retries")
			}
		})
	}
}
func TestPreviewReplacementExpirationAndClear(t *testing.T) {
	f := &FakeAIProvider{}
	s := newTestService(t, f)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	p, _ := s.Prepare(ctx, "a", Draft, sampleRequest())
	q, _ := s.Prepare(ctx, "a", Draft, sampleRequest())
	if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
		t.Fatal("superseded approval")
	}
	now = now.Add(5 * time.Minute)
	if _, e := s.Execute(ctx, "a", q.ID, true); e != ErrConsent {
		t.Fatal("expired approval")
	}
	p, _ = s.Prepare(ctx, "a", Draft, sampleRequest())
	s.Clear()
	if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
		t.Fatal("revoked approval")
	}
	if f.calls.Load() != 0 {
		t.Fatal("unapproved network")
	}
}
func TestClearCancelsAndSuppressesLateOutput(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &FakeAIProvider{invoke: func(ctx context.Context, _ Operation, _ Request) (Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return Result{NewText("late private result")}, nil
	}}
	s := newTestService(t, f)
	ctx := context.Background()
	p, _ := s.Prepare(ctx, "a", Draft, sampleRequest())
	done := make(chan error, 1)
	go func() { _, e := s.Execute(ctx, "a", p.ID, true); done <- e }()
	<-started
	if _, e := s.Prepare(ctx, "a", Draft, sampleRequest()); e != ErrBusy {
		t.Fatal(e)
	}
	if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrBusy {
		t.Fatal(e)
	}
	s.Clear()
	close(release)
	if e := <-done; e != ErrConsent {
		t.Fatal("late result not suppressed", e)
	}
	if _, e := s.Prepare(ctx, "a", Draft, sampleRequest()); e != nil {
		t.Fatal("service did not recover", e)
	}
}
func TestServiceValidationAndSafeErrors(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		result    string
		err, want error
	}{
		{"", errors.New("private prompt and secret"), ErrUnavailable},
		{"", fmt.Errorf("private: %w", ErrRateLimited), ErrRateLimited},
		{"", nil, ErrResponse}, {strings.Repeat("a", MaxOutputBytes+1), nil, ErrResponse},
		{"bad\x00", nil, ErrResponse}, {"\xff", nil, ErrResponse},
	} {
		f := &FakeAIProvider{invoke: func(context.Context, Operation, Request) (Result, error) { return Result{NewText(tc.result)}, tc.err }}
		s := newTestService(t, f)
		p, _ := s.Prepare(ctx, "a", Draft, sampleRequest())
		if _, e := s.Execute(ctx, "a", p.ID, true); e != tc.want {
			t.Fatal(e)
		}
		if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
			t.Fatal("failure replayed")
		}
	}
	f := &FakeAIProvider{}
	s := newTestService(t, f)
	for _, r := range []Request{{}, {NewText("a"), NewText("b"), 0}, {NewText("a"), NewText("b"), MaxOutputTokens + 1}, {NewText("\xff"), NewText("b"), 1}, {NewText("a"), NewText(strings.Repeat("x", MaxInputBytes)), 1}} {
		if _, e := s.Prepare(ctx, "a", Draft, r); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	if _, e := s.Prepare(ctx, "a", Operation("unknown"), sampleRequest()); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := s.Prepare(ctx, "", Draft, sampleRequest()); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := s.Prepare(ctx, "a", Summary, Request{Instruction: NewText("summarize"), MaxOutputTokens: 1}); e != ErrInvalid {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.Prepare(cancelled, "a", Draft, sampleRequest()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid dispatch")
	}
}
func TestTextDoesNotLeakThroughDiagnostics(t *testing.T) {
	canary := "synthetic-private-content-canary"
	for _, v := range []any{NewText(canary), Request{NewText(canary), NewText(canary), 1}, Result{NewText(canary)}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(format, v), canary) {
				t.Fatal("format leaked")
			}
		}
		raw, _ := json.Marshal(v)
		if bytes.Contains(raw, []byte(canary)) {
			t.Fatal("JSON leaked")
		}
		var out bytes.Buffer
		slog.New(slog.NewJSONHandler(&out, nil)).Info("fixture", "value", v)
		if strings.Contains(out.String(), canary) {
			t.Fatal("log leaked")
		}
	}
}
