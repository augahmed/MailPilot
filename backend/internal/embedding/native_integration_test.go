//go:build darwin && arm64 && cgo && embedding_native

package embedding

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeTestProvider(t *testing.T) *Provider {
	t.Helper()
	root, err := filepath.Abs("../../../src-tauri/resources/embedding")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal("run npm run prepare:embedding before native tests")
	}
	p, err := newProvider(func(ctx context.Context) (engine, error) { return loadNativeResources(ctx, root) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return p
}
func cosine(a, b []float32) float64 {
	result := float64(0)
	for i := range a {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}
func TestRealNativeMiniLMInferenceAndBatchParity(t *testing.T) {
	p := nativeTestProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	texts := []string{"The project review meeting is scheduled for Friday afternoon.", "A chocolate cake recipe requires flour, eggs and cocoa."}
	vectors, err := p.EmbedDocument(ctx, texts)
	if err != nil {
		t.Fatal("real ONNX inference failed", err)
	}
	query, err := p.EmbedQuery(ctx, "When is the project meeting?")
	if err != nil {
		t.Fatal(err)
	}
	related, unrelated := cosine(query, vectors[0]), cosine(query, vectors[1])
	if related <= unrelated+.15 {
		t.Fatalf("synthetic relevance: related %.4f, unrelated %.4f", related, unrelated)
	}
	for i, text := range texts {
		single, err := p.EmbedQuery(ctx, text)
		if err != nil {
			t.Fatal(err)
		}
		if len(single) != 384 || validateVector(single) != nil {
			t.Fatal("invalid real vector")
		}
		for d := range single {
			if math.Abs(float64(single[d]-vectors[i][d])) > 2e-5 {
				t.Fatal("padding/batch results differ")
			}
		}
	}
	// Exercise the longest permitted sequence and maximum simultaneous batch.
	long := strings.TrimSpace(strings.Repeat("hello ", 254))
	if _, err = p.EmbedDocument(ctx, []string{long, long, long, long}); err != nil {
		t.Fatal("maximum native batch failed", err)
	}
}
func TestRealNativeCancellationDoesNotPoisonSession(t *testing.T) {
	p := nativeTestProvider(t)
	if _, err := p.EmbedQuery(context.Background(), "warm up the native session"); err != nil {
		t.Fatal(err)
	}
	long := strings.TrimSpace(strings.Repeat("meeting ", 254))
	batch, err := p.tokenize(context.Background(), []string{long, long, long, long})
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = session.Run(ctx, batch)
	if err == nil || ctx.Err() == nil || time.Since(start) > 2*time.Second {
		t.Fatal("native deadline was not respected", err)
	}
	if _, err = p.EmbedQuery(context.Background(), "a subsequent healthy query"); err != nil {
		t.Fatal("cancellation poisoned the session", err)
	}
}
