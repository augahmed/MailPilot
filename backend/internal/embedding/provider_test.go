package embedding

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mailpilot.local/core/internal/embedding/model"
)

type fakeEngine struct {
	run    func(context.Context, tokenBatch) ([]float32, error)
	closed atomic.Int32
}

func (f *fakeEngine) Run(ctx context.Context, b tokenBatch) ([]float32, error) { return f.run(ctx, b) }
func (f *fakeEngine) Close() error                                             { f.closed.Add(1); return nil }
func fakeOutput(b tokenBatch) []float32 {
	out := make([]float32, b.Rows*b.Length*model.Dimensions)
	for row := 0; row < b.Rows; row++ {
		for token := 0; token < b.Length; token++ {
			start := (row*b.Length + token) * model.Dimensions
			out[start] = 3
			out[start+1] = 4
		}
	}
	return out
}
func fakeProvider(t *testing.T, run func(context.Context, tokenBatch) ([]float32, error)) (*Provider, *fakeEngine) {
	t.Helper()
	e := &fakeEngine{run: run}
	p, err := newProvider(func(context.Context) (engine, error) { return e, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := p.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return p, e
}
func TestLazyInitializationPoolingMaskAndIdentity(t *testing.T) {
	var loaded atomic.Int32
	e := &fakeEngine{run: func(_ context.Context, b tokenBatch) ([]float32, error) {
		if b.Rows != 2 || b.IDs[0] != 101 || b.Types[0] != 0 {
			t.Fatal("invalid model tensor inputs")
		}
		out := fakeOutput(b)
		for row := 0; row < b.Rows; row++ {
			for token := 0; token < b.Length; token++ {
				start := (row*b.Length + token) * model.Dimensions
				out[start] = 0
				out[start+1] = 4
				if token == 0 {
					out[start] = 3
					out[start+1] = 0
				}
			}
		}
		// Masked positions must not influence the mean, even if padding is non-finite.
		for row := 0; row < b.Rows; row++ {
			for token := 0; token < b.Length; token++ {
				if b.Mask[row*b.Length+token] == 0 {
					out[(row*b.Length+token)*model.Dimensions] = float32(math.NaN())
				}
			}
		}
		return out, nil
	}}
	p, err := newProvider(func(context.Context) (engine, error) { loaded.Add(1); return e, nil })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Load() != 0 {
		t.Fatal("eager inference initialization")
	}
	descriptor := p.Descriptor()
	if descriptor.Dimensions != 384 || len(descriptor.PreprocessingFingerprint) != 64 || descriptor.ModelSHA256 == "" || !descriptor.SymmetricQueries {
		t.Fatal("incomplete model identity")
	}
	vectors, err := p.EmbedDocument(context.Background(), []string{"hello", "a longer synthetic document"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Load() != 1 || len(vectors) != 2 || len(vectors[0]) != 384 || math.Abs(float64(vectors[0][0])-3/math.Sqrt(73)) > 1e-5 || math.Abs(float64(vectors[0][1])-8/math.Sqrt(73)) > 1e-5 {
		t.Fatal("incorrect masked mean/L2 normalization")
	}
	if err = p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = p.Close(context.Background()); err != nil || e.closed.Load() != 1 {
		t.Fatal("non-idempotent cleanup", err)
	}
	_, err = p.EmbedQuery(context.Background(), "after close")
	if !errors.Is(err, ErrClosed) {
		t.Fatal("closed provider ran", err)
	}
}
func TestInvalidInputDoesNotInitialize(t *testing.T) {
	var loaded atomic.Int32
	p, err := newProvider(func(context.Context) (engine, error) { loaded.Add(1); return nil, ErrUnavailable })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	for _, texts := range [][]string{nil, {""}, {"   "}, {string([]byte{255})}, {strings.Repeat("x", MaxInputBytes+1)}, {strings.Repeat("hello ", 255)}, {"a", "b", "c", "d", "e"}} {
		if _, err = p.EmbedDocument(context.Background(), texts); !errors.Is(err, ErrInput) {
			t.Fatal("invalid input accepted", err)
		}
	}
	if loaded.Load() != 0 {
		t.Fatal("invalid input initialized native runtime")
	}
}
func TestInvalidModelOutputRejected(t *testing.T) {
	for _, kind := range []string{"shape", "nan", "infinity", "zero", "error"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := fakeProvider(t, func(_ context.Context, b tokenBatch) ([]float32, error) {
				out := fakeOutput(b)
				switch kind {
				case "shape":
					return out[:len(out)-1], nil
				case "nan":
					out[0] = float32(math.NaN())
				case "infinity":
					out[0] = float32(math.Inf(1))
				case "zero":
					clear(out)
				case "error":
					return nil, errors.New("private native diagnostics")
				}
				return out, nil
			})
			_, err := p.EmbedQuery(context.Background(), "synthetic")
			want := ErrVector
			if kind == "error" {
				want = ErrInference
			}
			if !errors.Is(err, want) {
				t.Fatal("invalid result accepted or diagnostics leaked", err)
			}
		})
	}
}
func TestCancellationDeadlineAndCleanShutdown(t *testing.T) {
	entered := make(chan struct{}, 1)
	p, e := fakeProvider(t, func(ctx context.Context, b tokenBatch) ([]float32, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	first := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := p.EmbedQuery(ctx, "running"); first <- err }()
	<-entered
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	_, err := p.EmbedQuery(deadline, "waiting for bounded session")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queue deadline ignored", err)
	}
	cancel()
	if err = <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("inference cancellation ignored", err)
	}
	go func() { _, err := p.EmbedQuery(context.Background(), "close active call"); first <- err }()
	<-entered
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err = p.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown did not cancel active call", err)
	}
	if e.closed.Load() != 1 {
		t.Fatal("native resources leaked")
	}
}
func TestCanceledCallerAndTimedCloseDuringLazyConstruction(t *testing.T) {
	start := make(chan struct{})
	finish := make(chan struct{})
	e := &fakeEngine{}
	p, err := newProvider(func(context.Context) (engine, error) { close(start); <-finish; return e, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := p.EmbedQuery(ctx, "initialize"); result <- err }()
	<-start
	cancel()
	if err = <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("caller could not cancel while native init continued", err)
	}
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if err = p.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close deadline ignored", err)
	}
	close(finish)
	if err = p.Close(context.Background()); err != nil || e.closed.Load() != 1 {
		t.Fatal("late initialization was not cleaned", err)
	}
}
func TestUnavailableAssetsFailClosed(t *testing.T) {
	p, err := newProvider(func(context.Context) (engine, error) { return nil, ErrUnavailable })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if _, err = p.EmbedQuery(context.Background(), "no model"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unavailable model did not fail closed", err)
	}
}
