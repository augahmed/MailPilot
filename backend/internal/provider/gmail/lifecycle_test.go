package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding/model"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
)

// The only remote endpoint is this synthetic GET-only Gmail server. The tests
// exercise the real Gmail decoder/history adapter, sync commits, shared chunker,
// coordinator, search and semantic classification without credentials or AI.
type lifecycleRemote struct {
	mu       sync.Mutex
	messages map[string]message
	refs     []string
	records  []map[string]any
	cursor   int
	fail     string
	full     int
	writes   int
	blockID  string
	entered  chan struct{}
	release  chan struct{}
}

func lifecycleMessage(id, body string, complete bool) message {
	m := message{ID: id, ThreadID: "thread-" + id, HistoryID: "100", InternalDate: "1700000000000", LabelIDs: []string{"INBOX"}}
	m.Payload.MIMEType = "text/plain"
	m.Payload.Headers = []header{{"Subject", "Project update " + id}, {"From", "Colleague <colleague@example.test>"}}
	m.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte(body))
	size := int64(len(body))
	if !complete {
		size += 100
	}
	m.Payload.Body.Size = &size
	return m
}
func (r *lifecycleRemote) change(id string, m *message, deleted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cursor++
	if m != nil {
		m.HistoryID = strconv.Itoa(r.cursor)
		r.messages[id] = *m
	}
	if deleted {
		delete(r.messages, id)
	}
	kind := "messages"
	var events any = []any{map[string]string{"id": id}}
	if deleted {
		kind = "messagesDeleted"
		events = []any{map[string]any{"message": map[string]string{"id": id}}}
	}
	r.records = append(r.records, map[string]any{"id": strconv.Itoa(r.cursor), kind: events})
}
func (r *lifecycleRemote) serve(t *testing.T, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.Method != http.MethodGet {
		r.writes++
		w.WriteHeader(405)
		return
	}
	if req.Header.Get("Authorization") != "Bearer synthetic-test-token" {
		t.Error("unexpected credential")
	}
	switch req.URL.Path {
	case "/profile":
		_ = json.NewEncoder(w).Encode(map[string]string{"historyId": strconv.Itoa(r.cursor)})
	case "/labels":
		_, _ = w.Write([]byte(`{"labels":[{"id":"INBOX","name":"Inbox","type":"system"},{"id":"STARRED","name":"Starred","type":"system"},{"id":"DRAFT","name":"Draft","type":"system"}]}`))
	case "/messages":
		offset, _ := strconv.Atoi(req.URL.Query().Get("pageToken"))
		limit, _ := strconv.Atoi(req.URL.Query().Get("maxResults"))
		end := min(offset+limit, len(r.refs))
		refs := []map[string]string{}
		for _, id := range r.refs[offset:end] {
			refs = append(refs, map[string]string{"id": id})
		}
		next := ""
		if end < len(r.refs) {
			next = strconv.Itoa(end)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": refs, "nextPageToken": next})
	case "/history":
		start, _ := strconv.Atoi(req.URL.Query().Get("startHistoryId"))
		records := []map[string]any{}
		for _, rec := range r.records {
			n, _ := strconv.Atoi(rec["id"].(string))
			if n > start {
				records = append(records, rec)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"historyId": strconv.Itoa(r.cursor), "history": records})
	default:
		id := strings.TrimPrefix(req.URL.Path, "/messages/")
		if id == r.blockID && req.URL.Query().Get("format") == "minimal" {
			entered, release := r.entered, r.release
			r.mu.Unlock()
			close(entered)
			select {
			case <-req.Context().Done():
			case <-release:
			}
			r.mu.Lock()
			if req.Context().Err() != nil {
				return
			}
		}
		if id == r.fail {
			w.WriteHeader(400)
			return
		}
		m, ok := r.messages[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if req.URL.Query().Get("format") == "minimal" {
			m.Payload = part{}
		} else {
			r.full++
		}
		_ = json.NewEncoder(w).Encode(m)
	}
}

type lifecycleEmbedder struct {
	mu      sync.Mutex
	inputs  int
	calls   int
	blockAt int
	entered chan struct{}
	release chan struct{}
}

func (*lifecycleEmbedder) Descriptor() model.Descriptor { return model.Identity() }
func (*lifecycleEmbedder) Close(context.Context) error  { return nil }
func (f *lifecycleEmbedder) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.inputs += len(texts)
	f.calls++
	block := f.calls == f.blockAt
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if block {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
	}
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, 384)
		vectors[i][0] = 1
	}
	return vectors, nil
}
func (*lifecycleEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	v := make([]float32, 384)
	v[0] = 1
	return v, nil
}
func (f *lifecycleEmbedder) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.inputs }
func (f *lifecycleEmbedder) blockNext(at int) (<-chan struct{}, chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockAt = f.calls + at
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
	return f.entered, f.release
}

type lifecycleFixture struct {
	t        *testing.T
	dir      string
	db       *sqlite.Store
	remote   *lifecycleRemote
	server   *httptest.Server
	sync     *mailsync.Service
	index    *app.LocalIndex
	embedder *lifecycleEmbedder
	engine   *classification.SemanticClassifier
	profile  repository.IndexProfile
}

func newLifecycle(t *testing.T, messages ...message) *lifecycleFixture {
	t.Helper()
	h := &lifecycleFixture{t: t, dir: t.TempDir(), remote: &lifecycleRemote{messages: map[string]message{}, cursor: 100}, embedder: &lifecycleEmbedder{}}
	for _, m := range messages {
		h.remote.messages[m.ID] = m
		h.remote.refs = append(h.remote.refs, m.ID)
	}
	var err error
	h.db, err = sqlite.Open(context.Background(), h.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.AccountID{"a", "b"} {
		if _, err = h.db.Accounts().Upsert(context.Background(), domain.Account{ID: id, Provider: "gmail", ProviderAccountID: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.remote.serve(t, w, r) }))
	h.newSync(10)
	h.newIndex()
	t.Cleanup(func() {
		h.sync.Clear()
		if err := h.index.Close(context.Background()); err != nil {
			t.Error(err)
		}
		h.server.Close()
		_ = h.db.Close()
		h.remote.mu.Lock()
		defer h.remote.mu.Unlock()
		if h.remote.writes != 0 {
			t.Error("lifecycle attempted Gmail mutation")
		}
	})
	return h
}
func lifecycleSession() (domain.AccountID, context.Context, error) {
	return "a", context.Background(), nil
}
func (h *lifecycleFixture) newSync(limit int) {
	if h.sync != nil {
		h.sync.Clear()
	}
	h.sync = mailsync.New(context.Background(), lifecycleSession, func(a domain.AccountID) mailsync.Reader {
		p := NewProvider(a, syntheticTokens{})
		p.baseURL = h.server.URL
		p.client = h.server.Client()
		return p
	}, h.db.Emails(), limit)
	h.sync.SetCheckpoints(h.db.SyncCheckpoints())
	chunks, err := app.NewCachedChunker(h.db.Emails(), h.db.Chunks())
	if err != nil {
		h.t.Fatal(err)
	}
	h.sync.SetChunkProcessor(chunks.Prepare)
	h.sync.SetCachedChunkProcessor(chunks.PrepareCached)
}
func (h *lifecycleFixture) newIndex() {
	var err error
	h.index, err = app.NewLocalIndex(context.Background(), lifecycleSession, h.db.Emails(), h.db.Chunks(), h.db.Indexing(), h.embedder)
	if err != nil {
		h.t.Fatal(err)
	}
	h.index.EnableCategoryVectors(h.db.CategoryVectors())
	d := model.Identity()
	h.profile = repository.IndexProfile{Model: mailtext.ChunkModel(), PreprocessingVersion: mailtext.PreprocessingVersion, IndexVersion: mailtext.ChunkIndexVersion, Fingerprint: d.PreprocessingFingerprint, CategoryRecipe: repository.CategoryRecipe}
	h.engine, err = classification.NewSemanticClassifier(h.db.Semantic(), h.profile, nil, classification.DefaultSemanticPolicy())
	if err != nil {
		h.t.Fatal(err)
	}
	h.index.EnableClassification(h.db.Semantic(), h.engine)
}
func (h *lifecycleFixture) syncRun() mailsync.Progress {
	h.t.Helper()
	h.sync.Start()
	p := awaitSync(h.t, h.sync)
	if p.State != "completed" {
		h.t.Fatalf("sync: %+v", p)
	}
	return p
}
func (h *lifecycleFixture) command(op string) app.IndexResponse {
	return h.index.Manage(context.Background(), app.IndexRequest{Operation: op, AccountID: "a"})
}
func (h *lifecycleFixture) indexRun(op string) app.IndexResponse {
	h.t.Helper()
	h.command(op)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		p := h.command("status")
		if p.Status != "running" {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("index did not finish", h.command("status"))
	return app.IndexResponse{}
}
func (h *lifecycleFixture) stored(id string) repository.StoredEmail {
	h.t.Helper()
	e, err := h.db.Emails().GetByProviderID(context.Background(), "a", domain.ProviderMessageID(id))
	if err != nil {
		h.t.Fatal(err)
	}
	return e
}
func (h *lifecycleFixture) search(want int) retrieval.Result {
	h.t.Helper()
	result, err := retrieval.New(h.db.Vectors(), h.embedder).Search(context.Background(), "a", "project update", 20, repository.SearchFilter{})
	if err != nil || len(result.Emails) != want {
		h.t.Fatalf("search: %+v %v, want %d", result, err, want)
	}
	return result
}
func (h *lifecycleFixture) waitEntered(ch <-chan struct{}) {
	h.t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		h.t.Fatal("worker did not reach barrier")
	}
}
func (h *lifecycleFixture) assessment(id domain.EmailID) domain.ClassificationRun {
	h.t.Helper()
	r, err := h.db.Classifications().Get(context.Background(), "a", id)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func TestGmailLifecyclePartialImportRetryLabelsAndIdempotency(t *testing.T) {
	ctx := context.Background()
	first := lifecycleMessage("first", "The project review is scheduled for Friday. Please review the complete agenda.", true)
	partial := lifecycleMessage("partial", "", false)
	h := newLifecycle(t, first, partial)
	h.remote.fail = "partial"
	h.sync.Start()
	if p := awaitSync(t, h.sync); p.State != "completed" || p.Imported != 1 || p.Errors != 1 {
		t.Fatal(p)
	}
	e := h.stored("first")
	state, err := h.db.Chunks().State(ctx, "a", e.Email.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := h.db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || checkpoint.Phase != "history" || checkpoint.Cursor != "100" || len(checkpoint.RetryIDs) != 1 {
		t.Fatal(checkpoint, err)
	}
	if h.embedder.count() != 0 {
		t.Fatal("sync inferred")
	}
	h.remote.mu.Lock()
	h.remote.fail = ""
	h.remote.mu.Unlock()
	h.newSync(10) // Restart after a partial initial import.
	if p := h.syncRun(); p.Imported != 1 || p.Errors != 1 || p.Code != "body_unavailable" {
		t.Fatal(p)
	}
	after, err := h.db.Chunks().State(ctx, "a", e.Email.ID)
	if err != nil || after.Generation != state.Generation {
		t.Fatal("replay replaced current chunks", err)
	}
	if p := h.indexRun("start"); p.Status != "partial" || p.CompleteMessages != 1 || p.PartialMessages != 1 || p.IndexedMessages != 2 || p.AssessedMessages != 2 {
		t.Fatal(p)
	}
	h.search(1)
	missing := h.stored("partial")
	if r := h.assessment(missing.Email.ID); r.Semantic.State != "unclassified" {
		t.Fatal("incomplete body classified", r)
	}
	count := h.embedder.count()
	h.remote.mu.Lock()
	h.remote.messages["partial"] = lifecycleMessage("partial", "The complete project schedule is now available. Please review it before Friday.", true)
	h.remote.mu.Unlock()
	h.newSync(10)
	if p := h.syncRun(); p.Updated != 1 || p.Errors != 0 {
		t.Fatal("incomplete body did not retry without history", p)
	}
	if p := h.command("status"); p.Status != "pending" || p.PendingAssessments != 1 || p.AssessedMessages != 1 || h.embedder.count() != count {
		t.Fatal(p)
	}
	h.search(1)
	if p := h.indexRun("resume"); p.Status != "complete" || p.AssessedMessages != 2 || h.embedder.count() != count+1 {
		t.Fatal("unchanged subject re-embedded", p)
	}
	result := h.search(2)
	current := h.assessment(e.Email.ID)
	count = h.embedder.count()
	first.LabelIDs = []string{"STARRED"} // Leaving Inbox retains the cached body.
	h.remote.change("first", &first, false)
	if p := h.syncRun(); p.Updated != 1 {
		t.Fatal(p)
	}
	labels := h.stored("first")
	if labels.ContentHash != e.ContentHash || labels.Email.BodyText != e.Email.BodyText || len(labels.Email.Labels) != 1 || labels.Email.Labels[0].ProviderID != "STARRED" {
		t.Fatal("label-only update changed content")
	}
	if _, err = h.db.Classifications().Get(ctx, "a", e.Email.ID); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("label inputs did not stale classification", err)
	}
	if err = h.db.Vectors().CheckVectorSources(ctx, "a", h.profile, repository.SearchFilter{}, result.Sources); err != nil {
		t.Fatal("label-only update invalidated vectors", err)
	}
	if p := h.indexRun("resume"); p.Status != "complete" || h.embedder.count() != count {
		t.Fatal(p)
	}
	current = h.assessment(e.Email.ID)
	chunker, err := mailtext.NewChunker()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := chunker.Build(ctx, h.stored("first"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := h.db.Chunks().Replace(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := h.db.Chunks().List(ctx, "a", e.Email.ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	batch := repository.EmbeddingBatch{State: saved}
	for _, chunk := range chunks {
		vector, err := h.db.Chunks().Embedding(ctx, "a", e.Email.ID, chunk.ID, h.profile.Model)
		if err != nil {
			t.Fatal(err)
		}
		batch.Embeddings = append(batch.Embeddings, vector)
	}
	if _, err := h.db.Chunks().SaveEmbeddings(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if replay := h.assessment(e.Email.ID); replay.InputHash != current.InputHash {
		t.Fatal("identical chunk/vector publication invalidated classification")
	}
	// A second history observation with identical metadata must not stale results.
	h.remote.change("first", &first, false)
	h.syncRun()
	replay := h.assessment(e.Email.ID)
	if replay.InputHash != current.InputHash || !replay.Timestamp.Equal(current.Timestamp) || h.command("status").PendingAssessments != 0 || h.embedder.count() != count {
		t.Fatal("no-op history invalidated assessment")
	}
	h.syncRun()
	if r := h.assessment(e.Email.ID); r.InputHash != current.InputHash {
		t.Fatal("empty history invalidated assessment")
	}
	foreign, err := retrieval.New(h.db.Vectors(), h.embedder).Search(ctx, "b", "project", 20, repository.SearchFilter{})
	if err != nil || len(foreign.Emails) != 0 {
		t.Fatal("cross-account search", err)
	}
}

func TestGmailLifecycleSourceReplacementAndDeletionFenceIndexing(t *testing.T) {
	ctx := context.Background()
	draft := lifecycleMessage("draft", "The old project review is scheduled for Friday. Please review the original agenda.", true)
	draft.LabelIDs = append(draft.LabelIDs, "DRAFT") // Gmail revisions drafts under history IDs.
	h := newLifecycle(t, draft, lifecycleMessage("other", "An independent project message remains available for local search and classification.", true))
	h.syncRun()
	entered, release := h.embedder.blockNext(1)
	h.command("start")
	h.waitEntered(entered)
	original := h.stored("draft")
	oldState, err := h.db.Chunks().State(ctx, "a", original.Email.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldChunks, err := h.db.Chunks().List(ctx, "a", original.Email.ID, repository.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	draft.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("The replacement project review is on Tuesday. Use the updated agenda for this meeting."))
	size := int64(len("The replacement project review is on Tuesday. Use the updated agenda for this meeting."))
	draft.Payload.Body.Size = &size
	h.remote.change("draft", &draft, false)
	h.syncRun()
	updated := h.stored("draft")
	if updated.ContentHash == original.ContentHash {
		t.Fatal("source was not refreshed")
	}
	h.search(0)
	vector := make([]float32, 384)
	vector[0] = 1
	_, err = h.db.Chunks().SaveEmbeddings(ctx, repository.EmbeddingBatch{State: oldState, Embeddings: []repository.ChunkEmbedding{{ChunkID: oldChunks[0].ID, Status: "ready", Vector: vector}}})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale worker published", err)
	}
	close(release)
	for deadline := time.Now().Add(10 * time.Second); h.command("status").Status == "running" && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if p := h.command("status"); p.Status != "complete" || p.AssessedMessages != 2 {
		t.Fatal(p)
	}
	result := h.search(2)
	categories, err := h.db.Categories().List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.db.Organization().Create(ctx, domain.OrganizationPlan{AccountID: "a", Kind: domain.ActionLabelDelta, Rows: []domain.OrganizationRow{{EmailID: updated.Email.ID, ProviderID: updated.Email.ProviderID, CategoryID: categories[0].ID, ProposedLabel: "MailPilot/" + categories[0].Name}}, Steps: []domain.OrganizationStep{}})
	if err != nil {
		t.Fatal(err)
	}
	assessed := h.assessment(updated.Email.ID)
	if assessed.Semantic.SourceRevision != updated.ContentHash {
		t.Fatal("classification used old source")
	}
	for _, e := range result.Emails {
		if e.EmailID == string(updated.Email.ID) {
			for _, ev := range e.Evidence {
				if ev.SourceRevision != updated.ContentHash || strings.Contains(ev.Excerpt, "original agenda") {
					t.Fatal("stale search evidence", ev)
				}
			}
		}
	}
	// Source changes after completion require explicit indexing and reuse the subject.
	before := h.embedder.count()
	draft.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("A final project agenda changes the meeting to Wednesday. Please review all current details."))
	size = int64(len("A final project agenda changes the meeting to Wednesday. Please review all current details."))
	h.remote.change("draft", &draft, false)
	h.sync.SetChunkProcessor(func(context.Context, domain.AccountID, domain.ProviderMessageID) error {
		return repository.ErrStorage
	})
	if p := h.syncRun(); p.Updated != 1 || p.Code != "chunking_failed" || p.Errors != 1 {
		t.Fatal("post-commit projection failure lost source or warning", p)
	}
	if _, err = h.db.Chunks().List(ctx, "a", updated.Email.ID, repository.Page{Limit: 100}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("old chunks remained readable after projection failure", err)
	}
	h.search(1)
	if err = h.db.Vectors().CheckVectorSources(ctx, "a", h.profile, repository.SearchFilter{}, result.Sources); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("retained search was not revoked", err)
	}
	if _, err = h.db.Classifications().Get(ctx, "a", updated.Email.ID); !errors.Is(err, repository.ErrNotFound) && !errors.Is(err, repository.ErrConflict) {
		t.Fatal("classification stayed current", err)
	}
	if h.embedder.count() != before {
		t.Fatal("sync started indexing")
	}
	h.newSync(10)
	h.syncRun() // Bounded cached preparation repairs the committed source without history.
	if h.embedder.count() != before {
		t.Fatal("cache repair started inference")
	}
	entered, release = h.embedder.blockNext(1)
	h.command("resume")
	h.waitEntered(entered)
	h.remote.change("draft", nil, true)
	if p := h.syncRun(); p.Removed != 1 {
		t.Fatal(p)
	}
	if p := h.command("status"); p.Status != "paused" || p.Code != "cache_changed" || p.EligibleMessages != 1 {
		t.Fatal(p)
	}
	close(release)
	// Resume joins the revoked worker and indexes only remaining cached messages.
	if p := h.indexRun("resume"); p.Status != "complete" || p.IndexedMessages != 1 || p.AssessedMessages != 1 {
		t.Fatal(p)
	}
	h.search(1)
	if _, err = h.db.Emails().Get(ctx, "a", updated.Email.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("deleted source readable", err)
	}
	if _, err = h.db.Chunks().State(ctx, "a", updated.Email.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("deleted index retained", err)
	}
	if retained, err := h.db.Organization().Get(ctx, "a", plan.ID); err != nil || retained.Digest != plan.Digest {
		t.Fatal("sync deletion changed immutable Gmail journal evidence", err)
	}
}

func TestGmailLifecycleCancelledHistoryAndInterruptedIndexRestart(t *testing.T) {
	ctx := context.Background()
	long := lifecycleMessage("long", strings.Repeat("A project meeting happens every Friday. ", 220), true)
	h := newLifecycle(t, long)
	h.syncRun()
	// Cancellation before a metadata response must not advance its history record.
	long.LabelIDs = []string{"INBOX", "STARRED"}
	h.remote.change("long", &long, false)
	checkpoint, err := h.db.SyncCheckpoints().Load(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	h.remote.mu.Lock()
	h.remote.blockID = "long"
	h.remote.entered = make(chan struct{})
	h.remote.release = make(chan struct{})
	blocked := h.remote.entered
	releaseHTTP := h.remote.release
	h.remote.mu.Unlock()
	h.sync.Start()
	h.waitEntered(blocked)
	h.sync.Cancel()
	if p := awaitSync(t, h.sync); p.State != "cancelled" {
		t.Fatal(p)
	}
	// Let the HTTP handler finish even if the client cancelled before sending FIN.
	close(releaseHTTP)
	next, err := h.db.SyncCheckpoints().Load(ctx, "a")
	if err != nil || !reflect.DeepEqual(next, checkpoint) {
		t.Fatal("cancel advanced history", err)
	}
	h.remote.mu.Lock()
	h.remote.blockID = ""
	h.remote.mu.Unlock()
	h.newSync(10)
	h.syncRun()
	entered, _ := h.embedder.blockNext(2)
	h.command("start")
	h.waitEntered(entered)
	if p := h.command("status"); p.EmbeddingsGenerated != 4 || p.IndexedMessages != 0 {
		t.Fatal(p)
	}
	h.search(0)
	if p := h.command("pause"); p.Status != "paused" {
		t.Fatal(p)
	}
	if p := h.command("cancel"); p.Status != "cancelled" {
		t.Fatal(p)
	}
	if err = h.index.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the next run's durable begin but before its worker.
	run, err := h.db.Indexing().Begin(ctx, "a", h.profile, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.db, err = sqlite.Open(ctx, h.dir)
	if err != nil {
		t.Fatal(err)
	}
	h.embedder = &lifecycleEmbedder{}
	h.newSync(10)
	h.newIndex()
	p := h.command("status")
	if p.Status != "interrupted" || p.Generation == run.Generation || p.EmbeddingsGenerated != 4 || p.IndexedMessages != 0 || h.embedder.count() != 0 {
		t.Fatal("restart lost durable batches or inferred", p)
	}
	p = h.indexRun("resume")
	definitions := 0
	categories, err := h.db.Categories().List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range categories {
		source, err := h.db.CategoryVectors().Source(ctx, "a", category.ID, h.profile)
		if err != nil || source.Cached == nil {
			t.Fatal("missing category prototype", err)
		}
		definitions += len(source.Cached.DefinitionInputs)
	}
	if p.Status != "complete" || p.AssessedMessages != 1 || h.embedder.count() != p.EmbeddingsGenerated-4+definitions {
		t.Fatal("resume re-embedded committed chunks", p)
	}
	h.search(1)
	current := h.stored("long")
	if r := h.assessment(current.Email.ID); r.Semantic.SourceRevision != current.ContentHash {
		t.Fatal("restart assessed wrong source")
	}
}

func TestGmailLifecycleCoverageRemainsConsistentDuringSync(t *testing.T) {
	messages := make([]message, 32)
	for i := range messages {
		messages[i] = lifecycleMessage("concurrent-"+strconv.Itoa(i), "The project agenda contains the current meeting schedule and review instructions.", true)
	}
	h := newLifecycle(t, messages...)
	h.newSync(32)
	h.sync.Start()
	polls := 0
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		p := h.command("status")
		if p.Code != "" || p.AssessedMessages+p.PendingAssessments != p.EligibleMessages || p.CompleteMessages+p.PartialMessages+p.FailedMessages+p.PendingMessages+p.UnavailableMessages != p.EligibleMessages || p.EmbeddingsGenerated > p.ChunksCreated || p.ReadyCategories+p.PartialCategories+p.PendingCategories != p.EligibleCategories {
			t.Fatal("inconsistent combined coverage during source commits", p)
		}
		polls++
		if h.sync.Status().State != "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := h.sync.Status(); p.State != "completed" || p.Imported != 32 {
		t.Fatal(p)
	}
	if p := h.command("status"); p.EligibleMessages != 32 || p.PendingAssessments != 32 || h.embedder.count() != 0 || polls < 2 {
		t.Fatal("polling lost coverage or started inference", p)
	}
	if p := h.indexRun("start"); p.Status != "complete" || p.AssessedMessages != 32 {
		t.Fatal(p)
	}
	h.search(20)
}
