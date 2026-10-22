//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
	"mailpilot.local/core/internal/store/sqlite"
)

var performanceOutput = flag.String("performance.output", "", "synthetic evaluation artifact directory (opt-in expensive test)")
var performanceCorpusOnly = flag.Bool("performance.corpus-only", false, "generate the fixed corpus without indexing")
var performanceReuse = flag.Bool("performance.reuse", false, "resume the retained synthetic checkpoint without regenerating vectors")

type corpusMessage struct {
	Email    domain.Email
	Category string
}

// Versioned arithmetic recipe: no wall clock, global RNG, private mail or network.
// Thread lengths are 1,1,2,2,3,4,6; length buckets deliberately include long tails.
func performanceCorpus() []corpusMessage {
	topics := []struct{ category, subject, body string }{
		{"Jobs", "Engineering interview availability", "Our recruiting team invites you to a software engineering interview. The hiring manager will discuss your application and the technical assessment. Please send your availability for the panel next Tuesday."},
		{"School", "Course registration and coursework", "Professor Chen has posted the biology course syllabus. Submit the laboratory assignment through the student portal before Friday. Campus office hours are available for questions about enrollment and academic deadlines."},
		{"Receipts", "Payment received for your order", "Thank you for your purchase. Your order has been paid in full and the receipt is attached to this confirmation. The total includes shipping and sales tax. Keep this purchase confirmation for your records."},
		{"Finance", "Monthly bank account statement", "Your checking account statement is ready. Review deposits, withdrawals, interest charges and the closing balance. The credit card payment is due next month. Contact your bank if an account transaction is unfamiliar."},
		{"Newsletters", "Weekly technology digest", "Welcome to the weekly engineering newsletter. This edition covers database design, software releases and community events. Read our editorial analysis and subscribe for next week's stories. Manage newsletter preferences using the subscription page."},
		{"Personal", "Family picnic this weekend", "Would you like to join our family picnic on Saturday? We can meet our friends at the park near the lake. Bring lunch and let me know whether the children will come. Looking forward to catching up."},
		{"", "Status note", "The blue folder is on the shelf. The reference has been updated and the next step is still undecided. Please keep this note for later. There is no additional information available at the moment."},
	}
	weights := []int{0, 1, 2, 2, 3, 4, 4, 5, 5, 6}
	out := make([]corpusMessage, 0, 10000)
	for thread := 0; len(out) < 10000; thread++ {
		topic := topics[weights[(thread*17+21)%len(weights)]]
		length := []int{1, 1, 2, 2, 3, 4, 6}[thread%7]
		for reply := 0; reply < length && len(out) < 10000; reply++ {
			i := len(out)
			bucket := (i*37 + 21) % 100
			paragraphs := 1
			switch {
			case bucket >= 95:
				paragraphs = 28 + i%12
			case bucket >= 80:
				paragraphs = 8 + i%8
			case bucket >= 45:
				paragraphs = 3 + i%4
			}
			ref := fmt.Sprintf("MPREF%06d", thread)
			var body strings.Builder
			fmt.Fprintf(&body, "Reference %s. Conversation update %d.\n\n", ref, reply+1)
			for p := 0; p < paragraphs; p++ {
				fmt.Fprintf(&body, "%s Item %d covers case %d and revision %d.\n\n", topic.body, p+1, thread+1000, reply+1)
			}
			if reply > 0 {
				fmt.Fprintf(&body, "On Monday, Alex wrote:\n> Previous update for %s: %s\n", ref, topic.body)
			}
			if i%11 == 0 {
				fmt.Fprintf(&body, "\n---------- Forwarded message ----------\nFrom: colleague@example.test\nSubject: Earlier context\n%s\n", topic.body)
			}
			body.WriteString("\n-- \nAlex\nSynthetic correspondence\n")
			e := domain.Email{ID: domain.EmailID(fmt.Sprintf("perf-%05d", i)), AccountID: "a", ProviderID: domain.ProviderMessageID(fmt.Sprintf("perf-%05d", i)), ProviderThreadID: domain.ProviderThreadID(fmt.Sprintf("thread-%05d", thread)), Subject: topic.subject + " " + ref, BodyText: body.String(), BodyState: "complete", Sender: domain.EmailAddress{Address: fmt.Sprintf("sender%d@example.test", thread%97)}, ReceivedAt: time.Unix(1735689600+int64(i)*3600, 0).UTC()}
			if reply > 0 {
				e.Subject = "Re: " + e.Subject
			}
			if i%5 == 0 {
				html := "<p>" + strings.ReplaceAll(e.BodyText, "\n", "<br>") + "</p>"
				e.BodyHTML = &html
			}
			out = append(out, corpusMessage{e, topic.category})
		}
	}
	return out
}

type performanceMeter struct {
	*embedding.Provider
	mu              sync.Mutex
	inputs, batches int
	duration        time.Duration
}

func (m *performanceMeter) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	v, err := m.Provider.EmbedDocument(ctx, texts)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.duration += time.Since(start)
	if err == nil {
		m.inputs += len(v)
		m.batches++
	}
	return v, err
}
func perfMS(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
func perfLatency(samples []float64) map[string]any {
	sort.Float64s(samples)
	return map[string]any{"samples": len(samples), "p50Ms": samples[(len(samples)-1)/2], "p95Ms": samples[(len(samples)*95-1)/100], "maxMs": samples[len(samples)-1]}
}
func perfDistribution(samples []float64) map[string]any {
	sort.Float64s(samples)
	var total float64
	for _, value := range samples {
		total += value
	}
	return map[string]any{"samples": len(samples), "mean": total / float64(len(samples)), "p50": samples[(len(samples)-1)/2], "p95": samples[(len(samples)*95-1)/100], "max": samples[len(samples)-1]}
}

// Full production coordinator/provider/store; only its session is synthetic.
// Run with scripts/evaluate-performance.mjs: sandbox denies all network operations.
func TestNativePerformance10K(t *testing.T) {
	if *performanceOutput == "" {
		t.Skip("opt-in 10,000-message evaluation")
	}
	ctx := context.Background()
	if err := os.MkdirAll(*performanceOutput, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		b, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(*performanceOutput, name), append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	corpus := performanceCorpus()
	f, err := os.Create(filepath.Join(*performanceOutput, "corpus-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	lengths, words := []float64{}, []float64{}
	threads, support := map[domain.ProviderThreadID]bool{}, map[string]int{}
	for _, m := range corpus {
		b, e := json.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		b = append(b, '\n')
		if _, e = f.Write(b); e != nil {
			t.Fatal(e)
		}
		hash.Write(b)
		lengths = append(lengths, float64(len(m.Email.BodyText)))
		words = append(words, float64(len(strings.Fields(m.Email.BodyText))))
		threads[m.Email.ProviderThreadID] = true
		support[m.Category]++
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	const corpusHash = "9fb8754eb91bdb6640d81f902914d2947522b127f54828ef7c52a3a7d0aa9e4c"
	if hex.EncodeToString(hash.Sum(nil)) != corpusHash {
		t.Fatalf("corpus changed: version its recipe and frozen hash: %x", hash.Sum(nil))
	}
	write("corpus.json", map[string]any{"recipe": "mailpilot-synthetic-10k-v1-seed21", "sha256": corpusHash, "messages": len(corpus), "threads": len(threads), "bodyBytes": perfDistribution(lengths), "bodyWords": perfDistribution(words), "categorySupport": support})
	if *performanceCorpusOnly {
		return
	}
	dir := t.TempDir()
	if *performanceReuse {
		dir = filepath.Join(*performanceOutput, "cached-corpus")
		if _, err = os.Stat(filepath.Join(dir, "mailpilot.db")); err != nil {
			t.Fatal("missing synthetic checkpoint", err)
		}
	}
	start := time.Now()
	db, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	emptyOpen := time.Since(start)
	t.Cleanup(func() { _ = db.Close() })
	for _, a := range []domain.AccountID{"a", "b"} {
		if _, err = db.Accounts().Upsert(ctx, domain.Account{ID: a, Provider: "gmail", ProviderAccountID: string(a)}); err != nil {
			t.Fatal(err)
		}
	}
	start = time.Now()
	for off := 0; off < len(corpus); off += 100 {
		batch := []domain.Email{}
		for _, m := range corpus[off:min(len(corpus), off+100)] {
			batch = append(batch, m.Email)
		}
		if _, err = db.Emails().UpsertBatch(ctx, "a", batch); err != nil {
			t.Fatal(err)
		}
	}
	importTime := time.Since(start)
	private := corpus[0].Email
	private.ID = "private-canary"
	private.AccountID = "b"
	private.ProviderID = "private-canary"
	if _, err = db.Emails().Upsert(ctx, private); err != nil {
		t.Fatal(err)
	}
	p, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	meter := &performanceMeter{Provider: p}
	idx := newTestIndex(t, db, meter)
	idx.EnableCategoryVectors(db.CategoryVectors())
	rules, err := classification.SemanticRules()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := classification.NewSemanticClassifier(db.Semantic(), idx.profile, rules, classification.DefaultSemanticPolicy())
	if err != nil {
		t.Fatal(err)
	}
	idx.EnableClassification(db.Semantic(), engine)
	initialCoverage := indexCommand(idx, "status")
	if initialCoverage.EligibleMessages != 10000 || meter.inputs != 0 || *performanceReuse && initialCoverage.IndexedMessages != 10000 {
		t.Fatal("startup inferred or scope mismatch", initialCoverage)
	}
	start = time.Now()
	indexCommand(idx, "start")
	var final IndexResponse
	for {
		time.Sleep(5 * time.Second)
		final = indexCommand(idx, "status")
		t.Logf("PERFORMANCE_PROGRESS elapsed=%s indexed=%d chunks=%d assessed=%d state=%s", time.Since(start).Round(time.Second), final.IndexedMessages, final.ChunksCreated, final.AssessedMessages, final.Status)
		if final.Status != "running" {
			break
		}
	}
	indexTime := time.Since(start)
	if final.Status != "complete" || final.IndexedMessages != 10000 || final.AssessedMessages != 10000 || final.EmbeddingsGenerated != final.ChunksCreated {
		t.Fatal("incomplete corpus", final)
	}
	<-idx.worker.done
	meter.mu.Lock()
	inputs, batches, inferenceTime := meter.inputs, meter.batches, meter.duration
	meter.mu.Unlock()
	report := map[string]any{"recipe": "mailpilot-synthetic-10k-v1-seed21", "reusedCheckpoint": *performanceReuse, "initialCoverage": initialCoverage, "goVersion": runtime.Version(), "cpus": runtime.NumCPU(), "descriptor": p.Descriptor(), "coverage": final, "averageChunksPerMessage": float64(final.ChunksCreated) / 10000, "databaseOpenMs": perfMS(emptyOpen), "syntheticImportMs": perfMS(importTime), "indexMs": perfMS(indexTime), "inferenceInputsIncludingCategories": inputs, "inferenceBatches": batches, "inferenceMs": perfMS(inferenceTime), "indexMessagesPerSecond": float64(10000-initialCoverage.AssessedMessages) / indexTime.Seconds()}
	if inferenceTime > 0 {
		report["inferenceInputsPerSecond"] = float64(inputs) / inferenceTime.Seconds()
	}
	if *performanceReuse && inputs != 0 {
		t.Fatal("resume regenerated compatible document/category vectors", inputs)
	}
	write("performance.json", report)
	// Recompute every snapshot and verify every persisted vector via public APIs.
	start = time.Now()
	verified := 0
	for _, m := range corpus {
		stored, e := db.Emails().Get(ctx, "a", m.Email.ID)
		if e != nil {
			t.Fatal(e)
		}
		snapshot, e := idx.chunker.Build(ctx, stored)
		if e != nil {
			t.Fatal(e)
		}
		state, e := db.Chunks().State(ctx, "a", m.Email.ID)
		if e != nil || state.SourceRevision != stored.ContentHash || state.Status != "ready" {
			t.Fatal("source/state mismatch", m.Email.ID, e)
		}
		chunks, e := db.Chunks().List(ctx, "a", m.Email.ID, repository.Page{Limit: 100})
		if e != nil || len(chunks) != len(snapshot.Chunks) {
			t.Fatal("chunk mismatch", m.Email.ID, e)
		}
		for i, c := range chunks {
			want := snapshot.Chunks[i]
			if c.Text != want.Text || c.ContextText != want.ContextText || c.SourceRevision != snapshot.SourceRevision || c.StartOffset != want.StartOffset || c.EndOffset != want.EndOffset || c.PreprocessingVersion != snapshot.PreprocessingVersion {
				t.Fatal("projection mismatch", m.Email.ID)
			}
			v, e := db.Chunks().Embedding(ctx, "a", m.Email.ID, c.ID, state.Model)
			if e != nil || !validIndexVector(v.Vector, 384) || v.Status != "ready" {
				t.Fatal("vector mismatch", e)
			}
			verified++
		}
	}
	report["verifiedSourceVectors"] = verified
	report["consistencyCheckMs"] = perfMS(time.Since(start))
	if _, err = db.Chunks().State(ctx, "b", private.ID); err == nil {
		t.Fatal("other account indexed")
	}
	if got := idx.Manage(ctx, IndexRequest{Operation: "start", AccountID: "b"}); got.Code != "unauthorized" {
		t.Fatal("wrong account accepted")
	}
	// Corpus quality: frozen production policy, no fitting to generated labels.
	categories, err := db.Categories().List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	names := map[domain.CategoryID]string{}
	for _, c := range categories {
		names[c.ID] = c.Name
	}
	metrics := classificationMetrics{Categories: map[string]categoryMetric{}, Confusion: map[string]map[string]int{}}
	for _, c := range categories {
		metrics.Categories[c.Name] = categoryMetric{}
	}
	for _, m := range corpus {
		run, e := db.Classifications().Get(ctx, "a", m.Email.ID)
		if e != nil {
			t.Fatal(e)
		}
		metrics.Total++
		got := map[string]bool{}
		for _, r := range run.Results {
			got[names[r.CategoryID]] = true
		}
		if len(got) == 0 {
			metrics.Abstained++
		} else {
			metrics.Accepted++
		}
		if len(got) > 1 {
			metrics.MultipleSuggested++
		}
		metrics.SuggestedLabels += len(got)
		if m.Category != "" && got[m.Category] {
			metrics.CorrectLabels++
			if len(got) == 1 {
				metrics.Correct++
			}
		}
		expected := m.Category
		if expected == "" {
			expected = "Unclassified"
		}
		if metrics.Confusion[expected] == nil {
			metrics.Confusion[expected] = map[string]int{}
		}
		if len(got) == 0 {
			metrics.Confusion[expected]["Unclassified"]++
		}
		for name := range got {
			metrics.Confusion[expected][name]++
		}
		for name, c := range metrics.Categories {
			if m.Category == name {
				c.Support++
				if got[name] {
					c.TruePositive++
				} else {
					c.FalseNegative++
				}
			} else if got[name] {
				c.FalsePositive++
			}
			metrics.Categories[name] = c
		}
	}
	for name, c := range metrics.Categories {
		if c.TruePositive+c.FalsePositive > 0 {
			c.Precision = float64(c.TruePositive) / float64(c.TruePositive+c.FalsePositive)
		}
		if c.Support > 0 {
			c.Recall = float64(c.TruePositive) / float64(c.Support)
		}
		metrics.Categories[name] = c
	}
	metrics.Coverage = float64(metrics.Accepted) / float64(metrics.Total)
	metrics.AbstentionRate = float64(metrics.Abstained) / float64(metrics.Total)
	if metrics.SuggestedLabels > 0 {
		metrics.Precision = float64(metrics.CorrectLabels) / float64(metrics.SuggestedLabels)
	}
	write("corpus-classification.json", metrics)
	search := retrieval.New(db.Vectors(), p)
	report["search"] = performanceSearch(t, search, corpus)
	// Unchanged replay must reuse every vector; startup recovery must never infer.
	start = time.Now()
	indexCommand(idx, "start")
	<-idx.worker.done
	report["unchangedReplayMs"] = perfMS(time.Since(start))
	meter.mu.Lock()
	if meter.inputs != inputs {
		t.Fatal("replay regenerated vectors")
	}
	meter.mu.Unlock()
	if err = idx.Close(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := db.Indexing().Begin(ctx, "a", idx.profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "mailpilot.db"))
	if err != nil {
		t.Fatal(err)
	}
	report["checkpointedDatabaseBytes"] = info.Size()
	// Retain only this synthetic checkpoint so final-source query measurements
	// can be repeated without regenerating tens of thousands of native vectors.
	if !*performanceReuse {
		savedDir := filepath.Join(*performanceOutput, "cached-corpus")
		if err = os.MkdirAll(savedDir, 0700); err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(filepath.Join(dir, "mailpilot.db"))
		if err != nil {
			t.Fatal(err)
		}
		destination, err := os.OpenFile(filepath.Join(savedDir, "mailpilot.db"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(destination, source)
		source.Close()
		closeErr := destination.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal("retain synthetic checkpoint", copyErr, closeErr)
		}
	}
	start = time.Now()
	db, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	m2 := &performanceMeter{Provider: p2}
	idx2 := newTestIndex(t, db, m2)
	report["populatedDatabaseCoordinatorOpenMs"] = perfMS(time.Since(start))
	recovered := indexCommand(idx2, "status")
	if recovered.Status != "interrupted" || recovered.Generation == old.Generation || m2.inputs != 0 {
		t.Fatal("restart unsafe", recovered)
	}
	state, err := db.Chunks().State(ctx, "a", corpus[0].Email.ID)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := db.Chunks().List(ctx, "a", corpus[0].Email.ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	vector, err := db.Chunks().Embedding(ctx, "a", corpus[0].Email.ID, chunks[0].ID, state.Model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Indexing().SaveIndexed(ctx, old, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{vector}}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("stale worker accepted")
	}
	report["restartRequiresExplicitResume"] = true
	start = time.Now()
	indexCommand(idx2, "resume")
	<-idx2.worker.done
	report["restartResumeMs"] = perfMS(time.Since(start))
	if m2.inputs != 0 || indexCommand(idx2, "status").IndexedMessages != 10000 {
		t.Fatal("restart lost vectors")
	}
	if err = idx2.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var usage syscall.Rusage
	if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	report["peakRssBytes"] = usage.Maxrss
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	report["goHeapAllocBytes"] = memory.HeapAlloc
	write("performance.json", report)
	t.Log("PERFORMANCE_QUALIFICATION complete")
}

type performanceCancelMeter struct {
	*performanceMeter
	entered chan struct{}
	calls   int // one coordinator worker, read only after joining
}

func (m *performanceCancelMeter) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	m.calls++
	if m.calls == 2 {
		close(m.entered)
	}
	return m.performanceMeter.EmbedDocument(ctx, texts)
}

func TestNativePerformanceCancellation(t *testing.T) {
	if *performanceOutput == "" {
		t.Skip("opt-in real native cancellation measurement")
	}
	ctx := context.Background()
	initial, db, _, emails, dir := indexFixture(t, 1)
	if err := initial.Close(ctx); err != nil {
		t.Fatal(err)
	}
	e := emails[0]
	e.BodyText = strings.Repeat("The technical review meeting is Friday. Please confirm the updated project budget and agenda before the engineering team arrives. ", 100)
	if _, err := db.Emails().Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	p, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	meter := &performanceCancelMeter{performanceMeter: &performanceMeter{Provider: p}, entered: make(chan struct{})}
	idx := newTestIndex(t, db, meter)
	indexCommand(idx, "start")
	select {
	case <-meter.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("native second batch did not start")
	}
	time.Sleep(5 * time.Millisecond)
	start := time.Now()
	cancelled := indexCommand(idx, "cancel")
	commandTime := time.Since(start)
	select {
	case <-idx.worker.done:
	case <-time.After(5 * time.Second):
		t.Fatal("native cancellation did not join")
	}
	joinTime := time.Since(start)
	frozen := indexCommand(idx, "status")
	if cancelled.Status != "cancelled" || frozen.EmbeddingsGenerated != 4 || frozen.IndexedMessages != 0 || frozen.PartialMessages != 1 {
		t.Fatal("cancellation lost committed batch or published late data", frozen)
	}
	if err = idx.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a process dying with this partial message still in a running job.
	old, err := db.Indexing().Begin(ctx, "a", idx.profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p2, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	m2 := &performanceMeter{Provider: p2}
	resumed := newTestIndex(t, reopened, m2)
	recovered := indexCommand(resumed, "status")
	if recovered.Status != "interrupted" || recovered.Generation == old.Generation || recovered.EmbeddingsGenerated != 4 || m2.inputs != 0 {
		t.Fatal("restart checkpoint unsafe", recovered)
	}
	start = time.Now()
	indexCommand(resumed, "resume")
	final := waitIndex(t, resumed)
	<-resumed.worker.done
	resumeTime := time.Since(start)
	if final.Status != "complete" || final.IndexedMessages != 1 || m2.inputs != final.EmbeddingsGenerated-4 {
		t.Fatal("resume regenerated committed vectors", final)
	}
	result := map[string]any{"cancelCommandMs": perfMS(commandTime), "cancelAndJoinMs": perfMS(joinTime), "committedVectorsAfterCancel": 4, "lateVectorPublications": 0, "restartState": recovered.Status, "startupInferenceInputs": 0, "resumeMs": perfMS(resumeTime), "resumeGeneratedVectors": m2.inputs, "finalVectors": final.EmbeddingsGenerated}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*performanceOutput, "cancellation.json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("CANCELLATION_QUALIFICATION", string(raw))
}

func performanceSearch(t *testing.T, search *retrieval.VectorSearch, corpus []corpusMessage) []map[string]any {
	t.Helper()
	ctx := context.Background()
	var err error
	modeReports := []map[string]any{}
	for _, mode := range []string{"keyword", "semantic", "hybrid"} {
		latencies := []float64{}
		recall, ndcg, mrr := 0.0, 0.0, 0.0
		for qi := 0; qi < 12; qi++ {
			anchor := corpus[(qi*811+21)%len(corpus)].Email
			ref := strings.Fields(anchor.Subject)
			query := ref[len(ref)-1]
			relevant := map[string]bool{}
			for _, m := range corpus {
				if m.Email.ProviderThreadID == anchor.ProviderThreadID {
					relevant[string(m.Email.ID)] = true
				}
			}
			var result retrieval.Result
			for repeat := 0; repeat < 3; repeat++ {
				result, err = search.SearchMode(ctx, "a", query, 10, repository.SearchFilter{}, mode)
				if err != nil {
					t.Fatal(err)
				}
				latencies = append(latencies, perfMS(result.Elapsed))
			}
			hits, dcg, rr := 0, 0.0, 0.0
			for rank, e := range result.Emails {
				if e.EmailID == "private-canary" {
					t.Fatal("account leaked")
				}
				if relevant[e.EmailID] {
					hits++
					dcg += 1 / math.Log2(float64(rank+2))
					if rr == 0 {
						rr = 1 / float64(rank+1)
					}
				}
			}
			ideal := 0.0
			for i := 0; i < min(10, len(relevant)); i++ {
				ideal += 1 / math.Log2(float64(i+2))
			}
			recall += float64(hits) / float64(len(relevant))
			ndcg += dcg / ideal
			mrr += rr
		}
		modeReports = append(modeReports, map[string]any{"mode": mode, "latency": perfLatency(latencies), "identifierQueries": 12, "recallAt10": recall / 12, "ndcgAt10": ndcg / 12, "mrrAt10": mrr / 12})
	}
	return modeReports
}

func TestNativePerformanceSearch10K(t *testing.T) {
	if *performanceOutput == "" {
		t.Skip("opt-in search over retained synthetic corpus")
	}
	ctx := context.Background()
	dir := filepath.Join(*performanceOutput, "cached-corpus")
	if _, err := os.Stat(filepath.Join(dir, "mailpilot.db")); err != nil {
		t.Fatal("run the full corpus evaluation before --search-only", err)
	}
	db, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	corpus := performanceCorpus()
	first, err := db.Emails().Get(ctx, "a", corpus[0].Email.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := repository.EmailContentHash(corpus[0].Email)
	if err != nil || first.ContentHash != expected {
		t.Fatal("not the synthetic evaluation corpus", err)
	}
	p, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	search := retrieval.New(db.Vectors(), p)
	coverage, err := db.Indexing().Progress(ctx, "a", search.Profile)
	if err != nil || coverage.Eligible != 10000 || coverage.Complete != 10000 {
		t.Fatal("incomplete synthetic checkpoint", coverage, err)
	}
	report := map[string]any{"descriptor": p.Descriptor(), "coverage": coverage, "search": performanceSearch(t, search, corpus)}
	var usage syscall.Rusage
	if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	report["peakRssBytes"] = usage.Maxrss
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*performanceOutput, "search-10k-final.json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("PERFORMANCE_SEARCH_QUALIFICATION complete")
}

func TestNativePerformanceThroughput(t *testing.T) {
	if *performanceOutput == "" {
		t.Skip("opt-in real document-inference throughput")
	}
	ctx := context.Background()
	dir := filepath.Join(*performanceOutput, "cached-corpus")
	if _, err := os.Stat(filepath.Join(dir, "mailpilot.db")); err != nil {
		t.Fatal("missing synthetic checkpoint", err)
	}
	db, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	meter := &performanceMeter{Provider: p}
	corpus := performanceCorpus()
	first, err := db.Emails().Get(ctx, "a", corpus[0].Email.ID)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := repository.EmailContentHash(corpus[0].Email)
	if err != nil || first.ContentHash != hash {
		t.Fatal("not the synthetic corpus")
	}
	maxDelta := 0.0
	for _, m := range corpus {
		chunks, err := db.Chunks().List(ctx, "a", m.Email.ID, repository.Page{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for off := 0; off < len(chunks) && meter.inputs < 512; off += embedding.MaxBatch {
			texts := []string{}
			batch := chunks[off:min(len(chunks), off+embedding.MaxBatch, off+512-meter.inputs)]
			for _, c := range batch {
				texts = append(texts, mailtext.ModelInput(c.ChunkInput))
			}
			vectors, err := meter.EmbedDocument(ctx, texts)
			if err != nil {
				t.Fatal(err)
			}
			for i, c := range batch {
				saved, err := db.Chunks().Embedding(ctx, "a", m.Email.ID, c.ID, mailtext.ChunkModel())
				if err != nil || saved.Status != "ready" || len(saved.Vector) != len(vectors[i]) {
					t.Fatal("missing source-bound vector", err)
				}
				for d, value := range vectors[i] {
					delta := math.Abs(float64(value - saved.Vector[d]))
					maxDelta = math.Max(maxDelta, delta)
					if delta > 2e-5 {
						t.Fatal("stored vector differs from canonical source re-embedding", c.ID)
					}
				}
			}
		}
		if meter.inputs == 512 {
			break
		}
	}
	if meter.inputs != 512 {
		t.Fatal("insufficient representative inputs")
	}
	var usage syscall.Rusage
	if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"descriptor": p.Descriptor(), "inputs": meter.inputs, "batches": meter.batches, "providerCallMsIncludingInitialization": perfMS(meter.duration), "inputsPerSecond": float64(meter.inputs) / meter.duration.Seconds(), "peakRssBytes": usage.Maxrss, "numericParityVectors": meter.inputs, "maximumAbsoluteDifference": maxDelta, "recipe": "first-512-canonical-corpus-chunks-in-original-message-batches"}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(*performanceOutput, "embedding-throughput.json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("THROUGHPUT_QUALIFICATION complete")
}
