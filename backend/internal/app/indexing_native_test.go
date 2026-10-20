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
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
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

type cleanNativeIndexProvider struct {
	*embedding.Provider
	inputs []string
}

func (p *cleanNativeIndexProvider) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	for _, text := range texts {
		if strings.Contains(text, "[image:") || strings.Contains(text, "SyntheticTracking") || strings.Contains(text, "?qs=") || strings.Contains(text, "hidden-preheader") {
			return nil, embedding.ErrInput
		}
	}
	p.inputs = append(p.inputs, texts...)
	return p.Provider.EmbedDocument(ctx, texts)
}
func TestNativeLocalIndexCleanedNewsletter(t *testing.T) {
	initial, db, _, emails, _ := indexFixture(t, 3)
	_ = initial.Close(context.Background())
	e := emails[0]
	html := `<div style="display:none">hidden-preheader</div><img alt="Use code SAVE14"><p>Save 14% at Northshop</p><blockquote>Unique quoted appointment at noon.</blockquote>`
	e.BodyHTML = &html
	e.BodyText = "Selected offers October 5–16.\n[image: Example Bank logo]\n<https://click.example.test/u/?qs=" + strings.Repeat("SyntheticTracking", 12) + ">\n[image: Save 14% at Northshop]\n[image: Save 14% at Northshop]\n> Unique quoted appointment at noon."
	if _, err := db.Emails().Upsert(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fallback := emails[1]
	fallback.BodyHTML = &html
	var err error
	fallback.BodyText, err = mailtext.HTMLText(html)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Emails().Upsert(context.Background(), fallback); err != nil {
		t.Fatal(err)
	}
	multipart := emails[2]
	multipartHTML := `<p>Build your professional network.</p><p><b>Campus Connect</b> is free for students and alumni.</p><p>Email support@example.test for help.</p>`
	multipart.BodyHTML = &multipartHTML
	multipart.BodyText = "*Build your professional\nnetwork.*\n\n*Campus Connect\nhttps://click.engage.example.test/*\nis free for students and alumni.\n\nEmail support@example.test\n<support@example.test?utm_source=synthetic?subject=>\nfor help."
	if _, err = db.Emails().Upsert(context.Background(), multipart); err != nil {
		t.Fatal(err)
	}
	provider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	checked := &cleanNativeIndexProvider{Provider: provider}
	s := newTestIndex(t, db, checked)
	indexCommand(s, "start")
	p := waitIndex(t, s)
	<-s.worker.done
	if p.Status != "complete" || p.IndexedMessages != 3 || p.EmbeddingsGenerated == 0 {
		t.Fatalf("clean native indexing failed: %+v", p)
	}
	inputs := strings.Join(checked.inputs, "\n")
	if !strings.Contains(inputs, "Save 14% at Northshop") || !strings.Contains(inputs, "Unique quoted appointment at noon.") || !strings.Contains(inputs, "Use code SAVE14") {
		t.Fatal("real model missed cleaned offer/quote evidence")
	}
	if strings.Count(inputs, "Build your professional network.") != 1 || strings.Count(inputs, "Campus Connect") != 1 || strings.Contains(inputs, "utm_source=synthetic") || strings.Contains(inputs, "*Campus") {
		t.Fatal("duplicate plain/HTML representations reached real inference")
	}
	chunks, err := db.Chunks().List(context.Background(), "a", e.ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		vector, err := db.Chunks().Embedding(context.Background(), "a", e.ID, c.ID, s.profile.Model)
		if err != nil || vector.Status != "ready" || !validIndexVector(vector.Vector, 384) {
			t.Fatal("real clean vector was not persisted", err)
		}
	}
	reader := NewCachedReader(s.session, db.Emails(), nil, nil)
	read := reader.Read(context.Background(), ReadMailRequest{AccountID: "a", EmailID: string(e.ID)})
	if read.Code != "" || read.Message.BodyError || strings.Contains(read.Body, "SyntheticTracking") || strings.Contains(read.Body, "[image:") || !strings.Contains(read.Body, "Use code SAVE14") {
		t.Fatal("native cached reader did not use cleaned text", read.Code)
	}
	readFallback := reader.Read(context.Background(), ReadMailRequest{AccountID: "a", EmailID: string(fallback.ID)})
	if readFallback.Code != "" || strings.Contains(readFallback.Body, "hidden-preheader") || !strings.Contains(readFallback.Body, "Use code SAVE14") {
		t.Fatal("flattened HTML cache was not cleaned", readFallback.Code)
	}
	originalFallback, err := db.Emails().Get(context.Background(), "a", fallback.ID)
	if err != nil || originalFallback.Email.BodyText != fallback.BodyText {
		t.Fatal("raw HTML fallback changed", err)
	}
	stored, err := db.Emails().Get(context.Background(), "a", e.ID)
	if err != nil || stored.Email.BodyText != e.BodyText || stored.Email.BodyHTML == nil || *stored.Email.BodyHTML != html {
		t.Fatal("native indexing/reading overwrote original cache", err)
	}
}
