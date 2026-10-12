package sqlite

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/minilm"
	"mailpilot.local/core/internal/search"
)

// This fake tests retrieval mechanics; real semantic quality has a separate opt-in test.
type retrievalProvider struct{ emailTexts int }

func (p *retrievalProvider) ProviderName() string { return "fixture/retrieval-v1" }
func (p *retrievalProvider) Dimension() int       { return 5 }
func (p *retrievalProvider) EmbedText(ctx context.Context, s string) ([]float32, error) {
	v, err := p.EmbedBatch(ctx, []string{s})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (p *retrievalProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.HasPrefix(text, "Subject:") {
			p.emailTexts++
		}
		v := []float32{0, 0, 0, 0, .01}
		lower := strings.ToLower(text)
		for j, terms := range [][]string{{"interview", "hiring", "recruiter", "schedule"}, {"receipt", "purchase", "paid", "order"}, {"course", "class", "essay", "school"}, {"family", "picnic", "dinner"}} {
			for _, term := range terms {
				if strings.Contains(lower, term) {
					v[j]++
				}
			}
		}
		result[i] = v
	}
	return result, nil
}
func retrievalEmail(a domain.AccountID, id, subject, body, sender string, day int) domain.Email {
	return domain.Email{ID: domain.EmailID(id), AccountID: a, ProviderID: domain.ProviderMessageID(id), Subject: subject, BodyText: body, BodyState: "complete", Sender: domain.EmailAddress{Address: sender}, ReceivedAt: time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC)}
}
func retrievalFixture(t *testing.T) (*Store, *search.Service, *embedding.Service, *retrievalProvider, domain.AccountID) {
	t.Helper()
	s := testStore(t)
	a := account(t, s, "retrieval-a")
	b := account(t, s, "retrieval-b")
	saveEmail(t, s, retrievalEmail(a, "interview", "Meet the hiring team", "Sunny weather today.\nYour recruiter will schedule the technical interview on Tuesday.\n-- \nA long irrelevant signature.", "recruiter@example.test", 10))
	saveEmail(t, s, retrievalEmail(a, "receipt", "Purchase receipt", "Your order is paid and the purchase receipt is attached.", "store@example.test", 11))
	saveEmail(t, s, retrievalEmail(a, "school", "Essay deadline", "Your school class essay coursework is due tomorrow.", "teacher@example.test", 12))
	saveEmail(t, s, retrievalEmail(b, "foreign-interview", "Hiring interview schedule", "Interview recruiter hiring schedule.", "recruiter@example.test", 10))
	p := &retrievalProvider{}
	e, err := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedEmails(context.Background(), a, []domain.EmailID{"interview", "receipt", "school"}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedEmail(context.Background(), b, "foreign-interview"); err != nil {
		t.Fatal(err)
	}
	return s, search.New(s.SearchIndex(), e, nil), e, p, a
}
func TestLocalRetrievalQualityAndFilters(t *testing.T) {
	s, service, e, p, a := retrievalFixture(t)
	ctx := context.Background()
	result, err := service.Query(ctx, a, "technical hiring interview", 3, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 3 || result.Hits[0].Stored.Email.ID != "interview" || result.Hits[0].Similarity <= result.Hits[1].Similarity || !strings.Contains(result.Hits[0].Excerpt, "technical interview") {
		t.Fatal("relevance ranking/excerpt", result)
	}
	for _, hit := range result.Hits {
		if hit.Stored.Email.AccountID != a {
			t.Fatal("cross-account retrieval")
		}
	}
	before := p.emailTexts
	if _, err = e.EmbedEmails(ctx, a, []domain.EmailID{"interview", "receipt", "school"}); err != nil || p.emailTexts != before {
		t.Fatal("unchanged email re-embedded", err)
	}
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).UnixMilli()
	until := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).UnixMilli()
	result, err = service.Query(ctx, a, "purchase receipt", 20, search.Filters{From: &from, Until: &until, Sender: "STORE@EXAMPLE.TEST"})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Stored.Email.ID != "receipt" {
		t.Fatal("date/sender filter", result, err)
	}
	result, err = service.Query(ctx, a, "purchase", 3, search.Filters{Sender: "%_"})
	if err != nil || len(result.Hits) != 0 {
		t.Fatal("sender was interpreted as wildcard")
	}
	cats, _ := s.Categories().List(ctx, a)
	cat := cats[0].ID
	if err = s.Categories().Assign(ctx, a, "school", cat); err != nil {
		t.Fatal(err)
	}
	run := assessment(t, s, a, "interview", cat)
	if err = s.Classifications().Replace(ctx, run); err != nil {
		t.Fatal(err)
	}
	service.Model.ClassifierName = run.ClassifierName
	service.Model.ClassifierVersion = run.ClassifierVersion
	result, err = service.Query(ctx, a, "interview", 20, search.Filters{CategoryID: cat})
	if err != nil || len(result.Hits) != 2 {
		t.Fatal("assigned and predicted category", result, err)
	}
	c := cats[0]
	c.Description = "A modified definition"
	_, _ = s.Categories().Save(ctx, a, c)
	result, err = service.Query(ctx, a, "interview", 20, search.Filters{CategoryID: cat})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Stored.Email.ID != "school" {
		t.Fatal("stale category prediction returned", result, err)
	}
}
func TestSearchFreshnessCorruptionAndPurge(t *testing.T) {
	s, service, _, _, a := retrievalFixture(t)
	ctx := context.Background()
	counts, err := s.SearchIndex().Counts(ctx, a, service.Model)
	if err != nil || counts.Indexed != 3 {
		t.Fatal(counts, err)
	}
	e := retrievalEmail(a, "interview", "Changed topic", "A family picnic dinner invitation.", "recruiter@example.test", 10)
	saveEmail(t, s, e)
	counts, err = s.SearchIndex().Counts(ctx, a, service.Model)
	if err != nil || counts.Indexed != 2 {
		t.Fatal("stale index retained", counts, err)
	}
	result, err := service.Query(ctx, a, "interview", 3, search.Filters{})
	if err != nil || len(result.Hits) != 2 {
		t.Fatal(result, err)
	}
	data := make([]byte, 20)
	binary.LittleEndian.PutUint32(data, math.Float32bits(float32(math.NaN())))
	if _, err = s.db.Exec("UPDATE email_embeddings SET vector=? WHERE account_id=? AND email_id='receipt'", data, a); err != nil {
		t.Fatal(err)
	}
	result, err = service.Query(ctx, a, "receipt", 3, search.Filters{})
	if err != nil || !result.Partial || result.Errors != 1 || len(result.Hits) != 1 {
		t.Fatal("corrupt vector silently ranked", result, err)
	}
	if _, err = s.db.Exec("DELETE FROM emails WHERE account_id=? AND id='school'", a); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.SearchIndex().Counts(ctx, a, service.Model)
	if counts.Indexed != 0 {
		t.Fatal("purge or corrupt-vector removal retained vector")
	}
	if _, err = service.Query(ctx, a, "", 3, search.Filters{}); err != search.ErrInput {
		t.Fatal(err)
	}
	badFrom, badUntil := int64(20), int64(10)
	if _, err = service.Query(ctx, a, "query", 3, search.Filters{From: &badFrom, Until: &badUntil}); err != search.ErrInput {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = service.Query(ctx, a, "receipt", 3, search.Filters{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestLocalSearchIndexSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a := account(t, s, "restart-search")
	saveEmail(t, s, retrievalEmail(a, "one", "Interview", "Recruiter scheduling an interview.", "fixture@example.test", 10))
	p := &retrievalProvider{}
	e, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	if _, err = e.EmbedEmail(ctx, a, "one"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, _ = embedding.NewLocalService(p, s.Emails(), s.Vectors())
	service := search.New(s.SearchIndex(), e, nil)
	result, err := service.Query(ctx, a, "interview schedule", 1, search.Filters{})
	if err != nil || len(result.Hits) != 1 || p.emailTexts != 1 {
		t.Fatal("persistent index", result, err)
	}
}
func TestOptInMiniLMRetrievalQuality(t *testing.T) {
	python, model := os.Getenv("MAILPILOT_TEST_EMBEDDING_PYTHON"), os.Getenv("MAILPILOT_TEST_EMBEDDING_MODEL_DIR")
	if python == "" || model == "" {
		t.Skip("real local retrieval evaluation requires installed model")
	}
	ctx := context.Background()
	p, err := minilm.New(ctx, python, model)
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	a := account(t, s, "real-retrieval")
	corpus := []struct{ id, subject, body string }{
		{"interview", "Meet the engineering team", "The recruiter would like to arrange a technical interview for the developer position. Choose a meeting time with our hiring manager."},
		{"receipt", "Your order confirmation", "Payment received for your headphones purchase. Order number: SYNTHETIC-10. Total paid: 40 dollars. Keep this receipt."},
		{"school", "Homework deadline", "Your history class essay is due Friday. Submit the assignment through the course portal and contact your teacher for an extension."},
		{"personal", "Family dinner on Sunday", "Would you and the kids like to join us for dinner this weekend? We miss you. Let's enjoy a family gathering."},
	}
	var ids []domain.EmailID
	for _, item := range corpus {
		ids = append(ids, saveEmail(t, s, retrievalEmail(a, item.id, item.subject, item.body, "synthetic@example.test", 10)))
	}
	e, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	if _, err = e.EmbedEmails(ctx, a, ids); err != nil {
		t.Fatal(err)
	}
	service := search.New(s.SearchIndex(), e, nil)
	correct := 0
	for _, test := range []struct{ query, id string }{{"Scheduling a technical job interview", "interview"}, {"Proof that I paid for a purchase", "receipt"}, {"When to submit my university homework", "school"}, {"Getting together with relatives for a meal", "personal"}} {
		result, err := service.Query(ctx, a, test.query, 4, search.Filters{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Hits) != 4 || string(result.Hits[0].Stored.Email.ID) != test.id || result.Hits[0].Similarity <= result.Hits[len(result.Hits)-1].Similarity {
			t.Errorf("Relevant mail did not outrank unrelated mail for %q", test.query)
		} else {
			correct++
		}
		if len(result.Hits) > 0 {
			t.Logf("query=%q expected=%s first=%s cosine=%.3f", test.query, test.id, result.Hits[0].Stored.Email.ID, result.Hits[0].Similarity)
		}
	}
	t.Logf("MiniLM retrieval top-1: %d/4", correct)
}
