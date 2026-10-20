package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"mailpilot.local/core/internal/embedding"
)

// A fixed synthetic self-test for developers/package qualification only. It takes
// no text, model path, executable, URL, credentials or mailbox arguments; no IPC
// method exposes it. It never opens SQLite or starts authentication.
func embeddingSelfTest() int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := embedding.New()
	if err != nil {
		return 1
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = p.Close(closeCtx)
	}()
	runtime.GC()
	before := peakRSS()
	start := time.Now()
	documents, err := p.EmbedDocument(ctx, []string{"Subject: Project meeting\nSection: authored\nThe project review meeting is scheduled for Friday afternoon.", "Subject: Cooking\nSection: authored\nA chocolate cake recipe requires flour, eggs and cocoa."})
	if err != nil {
		return 1
	}
	cold := time.Since(start)
	start = time.Now()
	var query []float32
	for i := 0; i < 5; i++ {
		query, err = p.EmbedQuery(ctx, "When is the project meeting?")
		if err != nil {
			return 1
		}
	}
	warm := time.Since(start) / 5
	dot := func(a, b []float32) float64 {
		sum := float64(0)
		for i := range a {
			sum += float64(a[i]) * float64(b[i])
		}
		return sum
	}
	related, unrelated := dot(query, documents[0]), dot(query, documents[1])
	if related < unrelated+.15 || math.Abs(dot(query, query)-1) > 1e-4 {
		return 1
	}
	long := strings.TrimSpace(strings.Repeat("hello ", 254))
	start = time.Now()
	if _, err = p.EmbedDocument(ctx, []string{long, long, long, long}); err != nil {
		return 1
	}
	maximum := time.Since(start)
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := struct {
		OK                             bool    `json:"ok"`
		Descriptor                     any     `json:"descriptor"`
		ColdMilliseconds               float64 `json:"coldMilliseconds"`
		WarmQueryMilliseconds          float64 `json:"warmQueryMilliseconds"`
		MaxBatchMilliseconds           float64 `json:"maxBatchMilliseconds"`
		BaselinePeakRSSBytes           int64   `json:"baselinePeakRssBytes"`
		PeakRSSBytes                   int64   `json:"peakRssBytes"`
		GoHeapBytes                    uint64  `json:"goHeapBytes"`
		RelatedCosine, UnrelatedCosine float64
	}{true, p.Descriptor(), float64(cold.Microseconds()) / 1000, float64(warm.Microseconds()) / 1000, float64(maximum.Microseconds()) / 1000, before, peakRSS(), memory.HeapAlloc, related, unrelated}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return 1
	}
	if err = p.Close(ctx); err != nil {
		return 1
	}
	return 0
}
