package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/credentials"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSecrets struct {
	key   credentials.Secret
	err   error
	loads int
}

func (s *fakeSecrets) Load(context.Context) (credentials.Secret, error) {
	s.loads++
	return s.key, s.err
}
func (s *fakeSecrets) Save(_ context.Context, k credentials.Secret) error { s.key = k; return nil }
func (s *fakeSecrets) Delete(context.Context) error                       { s.key = credentials.Secret{}; return nil }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func syntheticRequest() ai.SummaryRequest {
	return ai.SummaryRequest{EmailID: "selected", Revision: strings.Repeat("a", 64), Email: ai.NewText("Synthetic email context"), KeyFingerprint: ai.KeyFingerprint(credentials.NewSecret("synthetic-key"))}
}

const complete = `{"status":"completed","output":[{"type":"reasoning"},{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"summary\":\"Synthetic answer\",\"requests\":[],\"deadlines\":[]}"}]}]}`

func responseBody(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func fixture(t *testing.T) (*Provider, *fakeSecrets) {
	t.Helper()
	s := &fakeSecrets{key: credentials.NewSecret("synthetic-key")}
	p, e := New(s, "fixture-model")
	if e != nil {
		t.Fatal(e)
	}
	return p, s
}
func TestSummaryWireContract(t *testing.T) {
	p, secrets := fixture(t)
	calls := 0
	p.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != endpoint || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Fatal("endpoint/credentials")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > time.Minute {
			t.Fatal("unbounded call")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got request
		if json.Unmarshal(raw, &got) != nil {
			t.Fatal("bad request")
		}
		if got.Model != "fixture-model" || got.Instructions != ai.Instructions() || got.Store || got.Stream || got.MaxOutputTokens != ai.MaxOutputTokens || len(got.Input) != 1 || got.Input[0].Content != "One untrusted cached email:\n"+syntheticRequest().Email.Reveal() {
			t.Fatal("incorrect summary payload")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		for _, key := range []string{"tools", "previous_response_id", "conversation", "metadata"} {
			if _, ok := fields[key]; ok {
				t.Fatal("unexpected tools/state")
			}
		}
		return responseBody(200, complete), nil
	})
	result, err := p.SummarizeEmail(context.Background(), syntheticRequest())
	if err != nil || result.Summary.Reveal() != "Synthetic answer" || calls != 1 || secrets.loads != 1 {
		t.Fatal(err)
	}
	if p.Identity() != (ai.Identity{Provider: "openai", Model: "fixture-model", External: true}) {
		t.Fatal("identity")
	}
}
func TestResponseFailuresAreBoundedRedactedAndNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"auth", 401, "private-key-and-email", ai.ErrAuthentication}, {"forbidden", 403, "private", ai.ErrAuthentication},
		{"rate", 429, "private", ai.ErrRateLimited}, {"server", 503, "private", ai.ErrUnavailable},
		{"redirect", 307, "private", ai.ErrUnavailable}, {"bad-json", 200, "private", ai.ErrResponse},
		{"empty", 200, `{"status":"completed","output":[]}`, ai.ErrResponse},
		{"too-big", 200, strings.Repeat("x", maxResponseBytes+1), ai.ErrResponse},
		{"invalid-utf8", 200, string([]byte{255}), ai.ErrResponse},
		{"incomplete", 200, `{"status":"incomplete"}`, ai.ErrIncomplete},
		{"failed", 200, `{"status":"failed","error":{"message":"private"}}`, ai.ErrResponse},
		{"refusal", 200, strings.Replace(complete, `"type":"output_text"`, `"type":"refusal"`, 1), ai.ErrRefused},
		{"tool", 200, `{"status":"completed","output":[{"type":"function_call","name":"send_mail"}]}`, ai.ErrResponse},
		{"wrong-role", 200, strings.Replace(complete, `"assistant"`, `"user"`, 1), ai.ErrResponse},
		{"long-text", 200, strings.Replace(complete, "Synthetic answer", strings.Repeat("x", ai.MaxOutputBytes+1), 1), ai.ErrResponse},
		{"nul", 200, strings.Replace(complete, "Synthetic answer", `bad\u0000text`, 1), ai.ErrResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := fixture(t)
			calls := 0
			p.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return responseBody(tc.status, tc.body), nil })
			result, e := p.SummarizeEmail(context.Background(), syntheticRequest())
			if e != tc.want || result.Summary.Reveal() != "" {
				t.Fatal("unsafe/incorrect failure", e)
			}
			if calls != 1 {
				t.Fatal("retried paid request")
			}
		})
	}
}
func TestCredentialFailuresAndRotation(t *testing.T) {
	p, s := fixture(t)
	calls := 0
	p.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+s.key.Reveal() {
			t.Fatal("stale credential")
		}
		return responseBody(200, complete), nil
	})
	for _, key := range []string{"synthetic-one", "synthetic-rotated"} {
		s.key = credentials.NewSecret(key)
		approved := syntheticRequest()
		approved.KeyFingerprint = ai.KeyFingerprint(s.key)
		if _, e := p.SummarizeEmail(context.Background(), approved); e != nil {
			t.Fatal(e)
		}
	}
	for _, key := range []string{"", "bad\nkey", "bad key", strings.Repeat("x", 2049)} {
		s.key = credentials.NewSecret(key)
		if _, e := p.SummarizeEmail(context.Background(), syntheticRequest()); e != ai.ErrCredentials {
			t.Fatal(e)
		}
	}
	s.err = errors.New("private-keyring-error")
	if _, e := p.SummarizeEmail(context.Background(), syntheticRequest()); e != ai.ErrCredentials {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("credential failure sent request")
	}
}
func TestSummaryRejectsKeyChangedAtDispatch(t *testing.T) {
	p, s := fixture(t)
	approved := syntheticRequest()
	s.key = credentials.NewSecret("synthetic-replacement")
	calls := 0
	p.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return responseBody(200, complete), nil
	})
	if _, err := p.SummarizeEmail(context.Background(), approved); err != ai.ErrConsent || calls != 0 {
		t.Fatal("changed key dispatched an approved payload", err)
	}
	approved.KeyFingerprint = [32]byte{}
	if _, err := p.SummarizeEmail(context.Background(), approved); err != ai.ErrConsent || calls != 0 {
		t.Fatal("missing key binding dispatched a payload", err)
	}
}
func TestNoRedirectsOrProxyAndSafeTransportError(t *testing.T) {
	p, _ := fixture(t)
	if p.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("environment proxy allowed")
	}
	calls := 0
	p.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		r := responseBody(307, "")
		r.Header.Set("Location", "https://example.test/steal")
		return r, nil
	})
	if _, e := p.SummarizeEmail(context.Background(), syntheticRequest()); e != ai.ErrUnavailable {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("followed redirect")
	}
	p.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("private payload and key") })
	if _, e := p.SummarizeEmail(context.Background(), syntheticRequest()); e != ai.ErrUnavailable {
		t.Fatal("transport detail leaked")
	}
}
func TestCancellationValidationAndConcurrencyBound(t *testing.T) {
	p, s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := p.SummarizeEmail(ctx, syntheticRequest()); e != context.Canceled {
		t.Fatal(e)
	}
	if _, e := p.SummarizeEmail(context.Background(), ai.SummaryRequest{}); e != ai.ErrInvalid {
		t.Fatal(e)
	}
	if s.loads != 0 {
		t.Fatal("invalid/cancelled call touched keys")
	}
	started := make(chan struct{})
	var calls atomic.Int32
	p.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	active, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := p.SummarizeEmail(active, syntheticRequest()); done <- e }()
	<-started
	waiting, end := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer end()
	if _, e := p.SummarizeEmail(waiting, syntheticRequest()); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	stop()
	if e := <-done; e != context.Canceled {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("unbounded concurrency")
	}
}
func TestConstructorAndOutputParts(t *testing.T) {
	for _, model := range []string{"", "bad model", "https://example.test", strings.Repeat("x", 129)} {
		if _, e := New(&fakeSecrets{}, model); e != ai.ErrInvalid {
			t.Fatal("bad configuration")
		}
	}
	if _, e := New(nil, "fixture"); e != ai.ErrInvalid {
		t.Fatal("missing store")
	}

	p, _ := fixture(t)
	for _, text := range []string{`{"summary":"ok","requests":["invented"],"deadlines":[]}`, `{"summary":"ok","requests":[],"deadlines":[],"tool":"send"}`, `{"summary":"ok"}`, `{"summary":"ok","requests":[],"deadlines":[]} {}`, `not JSON`} {
		p.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			var data response
			_ = json.Unmarshal([]byte(complete), &data)
			data.Output[1].Content[0].Text = text
			raw, _ := json.Marshal(data)
			return responseBody(200, string(raw)), nil
		})
		if _, err := p.SummarizeEmail(context.Background(), syntheticRequest()); err != ai.ErrResponse {
			t.Fatal("invalid normalized response", err)
		}
	}
}
