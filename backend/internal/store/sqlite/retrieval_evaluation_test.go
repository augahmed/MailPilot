package sqlite

import (
	"context"
	"encoding/json"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/minilm"
	"mailpilot.local/core/internal/search"
	"os"
	"testing"
	"time"
)

type timedRetrievalEncoder struct {
	search.Embeddings
	inference time.Duration
	version   string
}

func (e *timedRetrievalEncoder) PreprocessingVersion() string { return e.version }
func (e *timedRetrievalEncoder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	v, err := e.Embeddings.EmbedTexts(ctx, texts)
	e.inference += time.Since(start)
	return v, err
}

func TestOptInPassageRetrievalEvaluation(t *testing.T) {
	python, model := os.Getenv("MAILPILOT_TEST_EMBEDDING_PYTHON"), os.Getenv("MAILPILOT_TEST_EMBEDDING_MODEL_DIR")
	if python == "" || model == "" {
		t.Skip("requires installed local MiniLM")
	}
	var fixture struct {
		Messages []struct{ ID, Subject, Body, Thread string }
		Queries  []struct {
			Query    string
			Relevant []string
		}
	}
	raw, err := os.ReadFile("../../search/testdata/retrieval.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := minilm.New(ctx, python, model)
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	a := account(t, s, "passage-evaluation")
	ids := []domain.EmailID{}
	for _, item := range fixture.Messages {
		e := retrievalEmail(a, item.ID, item.Subject, item.Body, "synthetic@example.test", 10)
		e.ProviderThreadID = domain.ProviderThreadID(item.Thread)
		ids = append(ids, saveEmail(t, s, e))
	}
	base, _ := embedding.NewLocalService(p, s.Emails(), s.Vectors())
	modes := []struct {
		name    string
		encoder search.Embeddings
	}{{"baseline", base}, {"passages", &search.PassageEmbeddings{Base: base, Emails: s.Emails(), Vectors: s.PassageVectors()}}}
	for _, mode := range modes {
		started := time.Now()
		for i := 0; i < len(ids); i += embedding.MaxBatchSize {
			if _, err := mode.encoder.EmbedEmails(ctx, a, ids[i:min(i+embedding.MaxBatchSize, len(ids))]); err != nil {
				t.Fatal(err)
			}
		}
		version := embedding.PreprocessingVersion
		if mode.name == "passages" {
			version = embedding.RetrievalVersion
		}
		timed := &timedRetrievalEncoder{Embeddings: mode.encoder, version: version}
		service := search.New(s.SearchIndex(), timed, nil)
		top1, top3 := 0, 0
		queryStart := time.Now()
		for _, q := range fixture.Queries {
			result, err := service.Query(ctx, a, q.Query, 3, search.Filters{})
			if err != nil {
				t.Fatal(err)
			}
			rank := 0
			got := []string{}
			for i, hit := range result.Hits {
				got = append(got, string(hit.Stored.Email.ID))
				for _, id := range q.Relevant {
					if string(hit.Stored.Email.ID) == id && rank == 0 {
						rank = i + 1
					}
				}
			}
			if rank == 1 {
				top1++
			}
			if rank > 0 {
				top3++
			}
			if rank != 1 {
				t.Logf("%s failure query=%q relevant=%v got=%v rank=%d", mode.name, q.Query, q.Relevant, got, rank)
			}
		}
		t.Logf("%s messages=%d queries=%d top1=%d/%d top3=%d/%d elapsed=%s query_mean=%s local_scan_rank_mean=%s", mode.name, len(ids), len(fixture.Queries), top1, len(fixture.Queries), top3, len(fixture.Queries), time.Since(started), time.Since(queryStart)/time.Duration(len(fixture.Queries)), (time.Since(queryStart)-timed.inference)/time.Duration(len(fixture.Queries)))
	}
}
