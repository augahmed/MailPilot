package sqlite

import (
	"context"
	"errors"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/search"
	"strings"
	"testing"
)

type countedPassageProvider struct {
	retrievalProvider
	texts int
}

func (p *countedPassageProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	p.texts += len(texts)
	return p.retrievalProvider.EmbedBatch(ctx, texts)
}

func TestPassageIndexTailCacheIsolationAndInvalidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "passages")
	b := account(t, s, "other-passages")
	email := retrievalEmail(a, "long", "General report", strings.Repeat("Routine neutral administration notes for this month. ", 600)+"Interview recruiter hiring schedule at noon.", "test@example.test", 10)
	id := saveEmail(t, s, email)
	foreign := email
	foreign.AccountID = b
	foreign.ID = "foreign"
	saveEmail(t, s, foreign)
	p := &countedPassageProvider{}
	base, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	encoder := &search.PassageEmbeddings{Base: base, Emails: s.Emails(), Vectors: s.PassageVectors()}
	service := search.New(s.SearchIndex(), encoder, nil)
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{id}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(ctx, a, "interview recruiter", 3, search.Filters{})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].PassageKind != "body" || !strings.Contains(result.Hits[0].Excerpt, "Interview") {
		t.Fatal("tail not retrieved", err)
	}
	counts, _ := s.SearchIndex().Counts(ctx, b, service.Model)
	if counts.Indexed != 0 {
		t.Fatal("cross-account index")
	}
	before := p.texts
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{id}); err != nil || p.texts != before {
		t.Fatal("unchanged passages re-embedded", err)
	}
	cached, _ := s.PassageVectors().Cached(ctx, a, id, service.Model)
	doc, _ := embedding.PrepareRetrieval(email)
	for _, chunk := range doc.Passages {
		if cached[chunk.Hash] == nil {
			t.Fatal("missing persisted passage")
		}
	}
	email.BodyText += "\n> old quoted thread"
	saveEmail(t, s, email)
	counts, _ = s.SearchIndex().Counts(ctx, a, service.Model)
	if counts.Indexed != 1 {
		t.Fatal("quote-only edit invalidated retrieval")
	}
	email.Subject = "A changed subject"
	saveEmail(t, s, email)
	counts, _ = s.SearchIndex().Counts(ctx, a, service.Model)
	if counts.Indexed != 0 {
		t.Fatal("changed subject remained searchable")
	}
	remaining, _ := s.PassageVectors().Cached(ctx, a, id, service.Model)
	if len(remaining) == 0 || len(remaining) >= len(cached) {
		t.Fatal("unchanged body vectors were not preserved")
	}
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{id}); err != nil {
		t.Fatal(err)
	}
	source, _ := s.Emails().Get(ctx, a, id)
	prepared, _ := embedding.PrepareRetrieval(source.Email)
	vectors, _ := s.PassageVectors().Cached(ctx, a, id, service.Model)
	email.BodyText = "different body"
	saveEmail(t, s, email)
	if err := s.PassageVectors().Replace(ctx, source, service.Model, prepared, vectors); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("late stale index committed")
	}
}
func TestPassageDeduplicationAndRemotePurge(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "dedup")
	p := &countedPassageProvider{}
	base, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	encoder := &search.PassageEmbeddings{Base: base, Emails: s.Emails(), Vectors: s.PassageVectors()}
	service := search.New(s.SearchIndex(), encoder, nil)
	ids := []domain.EmailID{}
	for _, id := range []string{"original", "copy"} {
		e := retrievalEmail(a, id, "Interview schedule", "Recruiter interview on Tuesday.", "test@example.test", 10)
		e.ProviderThreadID = "thread"
		ids = append(ids, saveEmail(t, s, e))
	}
	if _, err := encoder.EmbedEmails(ctx, a, ids); err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(ctx, a, "interview", 3, search.Filters{})
	if err != nil || len(result.Hits) != 1 {
		t.Fatal("redundant thread results", err)
	}
	state := repository.SyncCheckpoint{Phase: "history", Cursor: "1"}
	if err := s.SyncCheckpoints().Commit(ctx, a, repository.SyncCheckpoint{}, state, nil, "original"); err != nil {
		t.Fatal(err)
	}
	values, _ := s.PassageVectors().Cached(ctx, a, "original", service.Model)
	if len(values) != 0 {
		t.Fatal("remote removal retained passage data")
	}
	count, _ := s.SearchIndex().Counts(ctx, a, service.Model)
	if count.Indexed != 1 {
		t.Fatal("removal retained manifest")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := encoder.EmbedEmails(cancelled, a, ids); !errors.Is(err, context.Canceled) {
		t.Fatal("index ignored cancellation")
	}
}

func TestPassageIndexRestartCorruptionAndFilters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := account(t, s, "restart-passages")
	email := retrievalEmail(a, "one", "Interview invitation", "The recruiter would like an interview.", "recruiter@example.test", 10)
	saveEmail(t, s, email)
	p := &countedPassageProvider{}
	base, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	encoder := &search.PassageEmbeddings{Base: base, Emails: s.Emails(), Vectors: s.PassageVectors()}
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{"one"}); err != nil {
		t.Fatal(err)
	}
	before := p.texts
	s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base, _ = embedding.NewLocalService(p, s.Emails(), s.Vectors())
	encoder = &search.PassageEmbeddings{Base: base, Emails: s.Emails(), Vectors: s.PassageVectors()}
	service := search.New(s.SearchIndex(), encoder, nil)
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{"one"}); err != nil || p.texts != before {
		t.Fatal("restart recomputed cached chunks", err)
	}
	cats, _ := s.Categories().List(ctx, a)
	if err := s.Categories().Assign(ctx, a, "one", cats[0].ID); err != nil {
		t.Fatal(err)
	}
	if r, err := service.Query(ctx, a, "recruiter", 3, search.Filters{CategoryID: cats[0].ID, Sender: "recruiter"}); err != nil || len(r.Hits) != 1 {
		t.Fatal("passage filters failed", err)
	}
	if r, err := service.Query(ctx, a, "recruiter", 3, search.Filters{Sender: "someoneelse"}); err != nil || len(r.Hits) != 0 {
		t.Fatal("passage sender filter leaked")
	}
	if _, err := s.db.Exec("UPDATE email_passage_vectors SET vector=zeroblob(dimension*4) WHERE account_id=?", a); err != nil {
		t.Fatal(err)
	}
	if r, err := service.Query(ctx, a, "recruiter", 3, search.Filters{}); err != nil || !r.Partial || len(r.Hits) != 0 || r.Errors != 1 {
		t.Fatal("corrupt passages ranked", err)
	}
	counts, _ := s.SearchIndex().Counts(ctx, a, service.Model)
	if counts.Indexed != 0 {
		t.Fatal("corrupt index marked complete")
	}
	if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{"one"}); err != nil {
		t.Fatal("could not rebuild corruption", err)
	}
}
