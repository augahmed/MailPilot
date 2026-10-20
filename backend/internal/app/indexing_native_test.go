//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mailpilot.local/core/internal/embedding"
)

type nativeIndexMeter struct {
	*embedding.Provider
	mu                 sync.Mutex
	generated, batches int
}

func (m *nativeIndexMeter) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := m.Provider.EmbedDocument(ctx, texts)
	if err == nil {
		m.mu.Lock()
		m.generated += len(vectors)
		m.batches++
		m.mu.Unlock()
	}
	return vectors, err
}

// Run only from the fixed packaged sidecar executable layout, under network deny.
// The test owns a temporary synthetic DB and never initializes Gmail/credentials.
func TestNativeLocalIndexSynthetic(t *testing.T) {
	initial, db, _, emails, dir := indexFixture(t, 200)
	_ = initial.Close(context.Background())
	for i := range emails {
		if i%5 == 0 {
			emails[i].BodyText = strings.Repeat("The project review meeting is Friday. Please confirm the agenda and bring the updated budget. ", 60)
		}
		if _, err := db.Emails().Upsert(context.Background(), emails[i]); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	meter := &nativeIndexMeter{Provider: provider}
	s := newTestIndex(t, db, meter)
	wait := func() IndexResponse {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			p := indexCommand(s, "status")
			if p.Status != "running" {
				return p
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("native index timed out")
		return IndexResponse{}
	}
	start := time.Now()
	indexCommand(s, "start")
	p := wait()
	cold := time.Since(start)
	if p.Status != "complete" || p.IndexedMessages != 200 || p.EmbeddingsGenerated != p.ChunksCreated {
		t.Fatalf("native pipeline failed: %+v", p)
	}
	meter.mu.Lock()
	generated, batches := meter.generated, meter.batches
	meter.mu.Unlock()
	start = time.Now()
	indexCommand(s, "start")
	again := wait()
	replay := time.Since(start)
	meter.mu.Lock()
	replayGenerated := meter.generated - generated
	meter.mu.Unlock()
	if again.Status != "complete" || replayGenerated != 0 {
		t.Fatal("native replay regenerated vectors")
	}
	var usage syscall.Rusage
	if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, name := range []string{"mailpilot.db", "mailpilot.db-wal"} {
		if info, e := os.Stat(filepath.Join(dir, name)); e == nil {
			bytes += info.Size()
		}
	}
	result := map[string]any{"messages": p.IndexedMessages, "chunks": p.ChunksCreated, "embeddings": generated, "inferenceBatches": batches, "indexMilliseconds": float64(cold.Microseconds()) / 1000, "messagesPerSecond": float64(p.IndexedMessages) / cold.Seconds(), "replayMilliseconds": float64(replay.Microseconds()) / 1000, "replayEmbeddings": replayGenerated, "peakRssBytes": usage.Maxrss, "databaseAndWalBytes": bytes}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("INDEX_QUALIFICATION", string(data))
}
