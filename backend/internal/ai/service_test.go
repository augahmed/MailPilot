package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mailpilot.local/core/internal/domain"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	calls  atomic.Int32
	invoke func(context.Context, SummaryRequest) (SummaryResult, error)
}

func (f *fakeProvider) SummarizeEmail(ctx context.Context, r SummaryRequest) (SummaryResult, error) {
	f.calls.Add(1)
	if f.invoke != nil {
		return f.invoke(ctx, r)
	}
	return sampleResult(), nil
}
func sampleRequest() SummaryRequest {
	return SummaryRequest{EmailID: "selected", Revision: strings.Repeat("a", 64), Email: NewText("Please confirm by Tuesday.")}
}
func sampleResult() SummaryResult {
	return SummaryResult{Summary: NewText("A confirmation is requested."), Requests: []Text{NewText("Please confirm")}, Deadlines: []Text{NewText("Tuesday")}}
}
func newTestService(t *testing.T, f AIProvider) *Service {
	t.Helper()
	s, err := NewService(f, Identity{Provider: "fake", Model: "fixture", External: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDisabledService(t *testing.T) {
	for _, s := range []*Service{nil, {}} {
		if s.Enabled() {
			t.Fatal("enabled")
		}
		if _, e := s.Prepare(context.Background(), "a", sampleRequest()); e != ErrDisabled {
			t.Fatal(e)
		}
		if _, e := s.Execute(context.Background(), "a", "id", true); e != ErrDisabled {
			t.Fatal(e)
		}
		s.Clear()
	}
}
func TestExactSingleUseApproval(t *testing.T) {
	f := &fakeProvider{invoke: func(_ context.Context, r SummaryRequest) (SummaryResult, error) {
		if r.Email.Reveal() != sampleRequest().Email.Reveal() {
			t.Fatal("payload changed")
		}
		return sampleResult(), nil
	}}
	s := newTestService(t, f)
	ctx := context.Background()
	p, err := s.Prepare(ctx, "a", sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 || p.Instructions.Reveal() != Instructions() {
		t.Fatal("undisclosed dispatch")
	}
	p.Request.Email = NewText("tampered")
	for _, v := range []struct {
		account, id string
		approved    bool
	}{{"b", p.ID, true}, {"a", "wrong", true}, {"a", p.ID, false}} {
		if _, err = s.Execute(ctx, domain.AccountID(v.account), v.id, v.approved); err != ErrConsent {
			t.Fatal(err)
		}
	}
	result, err := s.Execute(ctx, "a", p.ID, true)
	if err != nil || result.Summary.Reveal() == "" || f.calls.Load() != 1 {
		t.Fatal(err)
	}
	if _, err = s.Execute(ctx, "a", p.ID, true); err != ErrConsent {
		t.Fatal("replay")
	}
}
func TestReplacementExpiryAndClear(t *testing.T) {
	f := &fakeProvider{}
	s := newTestService(t, f)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	p, _ := s.Prepare(ctx, "a", sampleRequest())
	q, _ := s.Prepare(ctx, "a", sampleRequest())
	if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
		t.Fatal(e)
	}
	now = now.Add(5 * time.Minute)
	if _, e := s.Execute(ctx, "a", q.ID, true); e != ErrConsent {
		t.Fatal(e)
	}
	p, _ = s.Prepare(ctx, "a", sampleRequest())
	s.Clear()
	if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
		t.Fatal(e)
	}
	if f.calls.Load() != 0 {
		t.Fatal("unapproved call")
	}
}
func TestClearCancelsAndSuppressesLateOutput(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeProvider{invoke: func(ctx context.Context, _ SummaryRequest) (SummaryResult, error) {
		close(started)
		<-ctx.Done()
		<-release
		return sampleResult(), nil
	}}
	s := newTestService(t, f)
	ctx := context.Background()
	p, _ := s.Prepare(ctx, "a", sampleRequest())
	done := make(chan error, 1)
	go func() { _, e := s.Execute(ctx, "a", p.ID, true); done <- e }()
	<-started
	if _, e := s.Prepare(ctx, "a", sampleRequest()); e != ErrBusy {
		t.Fatal(e)
	}
	s.Clear()
	close(release)
	if e := <-done; e != ErrConsent {
		t.Fatal(e)
	}
}
func TestSummaryValidationAndRedactedFailures(t *testing.T) {
	ctx := context.Background()
	for _, request := range []SummaryRequest{{}, {EmailID: "a", Revision: "bad", Email: NewText("text")}, {EmailID: "a", Revision: strings.Repeat("a", 64), Email: NewText("\xff")}, {EmailID: "a", Revision: strings.Repeat("a", 64), Email: NewText(strings.Repeat("x", MaxInputBytes+1))}} {
		if request.Validate() != ErrInvalid {
			t.Fatal("bad source accepted")
		}
	}
	for _, tc := range []struct {
		result    SummaryResult
		err, want error
	}{{SummaryResult{}, errors.New("private"), ErrUnavailable}, {SummaryResult{}, nil, ErrResponse}, {SummaryResult{Summary: NewText("text"), Requests: []Text{NewText("invented")}, Deadlines: []Text{}}, nil, ErrResponse}, {sampleResult(), fmt.Errorf("private: %w", ErrRateLimited), ErrRateLimited}} {
		f := &fakeProvider{invoke: func(context.Context, SummaryRequest) (SummaryResult, error) { return tc.result, tc.err }}
		s := newTestService(t, f)
		p, _ := s.Prepare(ctx, "a", sampleRequest())
		if _, e := s.Execute(ctx, "a", p.ID, true); e != tc.want {
			t.Fatal(e)
		}
		if _, e := s.Execute(ctx, "a", p.ID, true); e != ErrConsent {
			t.Fatal("retried")
		}
	}
}
func TestTextDoesNotLeakThroughDiagnostics(t *testing.T) {
	canary := "synthetic-private-content-canary"
	for _, v := range []any{NewText(canary), SummaryRequest{EmailID: "id", Revision: strings.Repeat("a", 64), Email: NewText(canary)}, SummaryResult{Summary: NewText(canary), Requests: []Text{NewText(canary)}, Deadlines: []Text{}}} {
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

func TestProviderInterfaceHasOnlyTheSummaryCapability(t *testing.T) {
	contract := reflect.TypeFor[AIProvider]()
	if contract.NumMethod() != 1 || contract.Method(0).Name != "SummarizeEmail" {
		t.Fatal("generic AI operation exposed")
	}
}
