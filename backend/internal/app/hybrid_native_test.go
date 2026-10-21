//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

func TestNativeHybridSearchEvaluation(t *testing.T) {
	raw, err := os.ReadFile("../retrieval/testdata/hybrid-heldout-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version  int
		Messages []struct{ ID, Account, Subject, Body, Sender, Thread string }
		Queries  []struct {
			Kind, Query string
			Relevant    []string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initial, db, _, _, _ := indexFixture(t, 0)
	_ = initial.Close(ctx)
	chunker, err := NewCachedChunker(db.Emails(), db.Chunks())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range fixture.Messages {
		e := domain.Email{ID: domain.EmailID(m.ID), AccountID: domain.AccountID(m.Account), ProviderID: domain.ProviderMessageID(m.ID), ProviderThreadID: domain.ProviderThreadID(m.Thread), Subject: m.Subject, BodyText: m.Body, BodyState: "complete", Sender: domain.EmailAddress{Address: m.Sender}, ReceivedAt: time.Unix(1700000000, 0)}
		if _, err = db.Emails().Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err = chunker.Prepare(ctx, e.AccountID, e.ProviderID); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	idx := newTestIndex(t, db, provider)
	indexCommand(idx, "start")
	if got := waitIndex(t, idx); got.Status != "complete" {
		t.Fatal(got)
	}
	if err = idx.Close(ctx); err != nil {
		t.Fatal(err)
	}
	queryProvider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	defer queryProvider.Close(ctx)
	search := retrieval.New(db.Vectors(), queryProvider)
	reports := []map[string]any{}
	for _, mode := range []string{"keyword", "semantic", "hybrid"} {
		cold, err := search.SearchMode(ctx, "a", fixture.Queries[0].Query, 10, repository.SearchFilter{}, mode)
		if err != nil {
			t.Fatal(err)
		}
		recall, ndcg, mrr := 0.0, 0.0, 0.0
		answerable, exact, exactMisses, noAnswer, emptyNoAnswer := 0, 0, 0, 0, 0
		latencies := []float64{}
		cases := []map[string]any{}
		for _, q := range fixture.Queries {
			relevant := map[string]bool{}
			for _, id := range q.Relevant {
				relevant[id] = true
			}
			var result retrieval.Result
			for repeat := 0; repeat < 5; repeat++ {
				result, err = search.SearchMode(ctx, "a", q.Query, 10, repository.SearchFilter{}, mode)
				if err != nil {
					t.Fatal(err)
				}
				latencies = append(latencies, float64(result.Elapsed.Microseconds())/1000)
			}
			hits, dcg, rr := 0, 0.0, 0.0
			ids := []string{}
			for rank, e := range result.Emails {
				if e.EmailID == "private-hotel" {
					t.Fatal("cross-account leak")
				}
				ids = append(ids, e.EmailID)
				if relevant[e.EmailID] {
					hits++
					dcg += 1 / math.Log2(float64(rank+2))
					if rr == 0 {
						rr = 1 / float64(rank+1)
					}
				}
			}
			row := map[string]any{"kind": q.Kind, "query": q.Query, "relevant": q.Relevant, "top10": ids}
			if len(relevant) > 0 {
				answerable++
				ideal := 0.0
				for i := 0; i < min(10, len(relevant)); i++ {
					ideal += 1 / math.Log2(float64(i+2))
				}
				r, n := float64(hits)/float64(len(relevant)), dcg/ideal
				recall += r
				ndcg += n
				mrr += rr
				row["recallAt10"] = r
				row["ndcgAt10"] = n
			} else {
				noAnswer++
				if len(ids) == 0 {
					emptyNoAnswer++
				}
			}
			if q.Kind == "identifier" || q.Kind == "company" {
				exact++
				if len(ids) == 0 || !relevant[ids[0]] {
					exactMisses++
				}
			}
			cases = append(cases, row)
		}
		sort.Float64s(latencies)
		reports = append(reports, map[string]any{"mode": mode, "answerableQueries": answerable, "recallAt10": recall / float64(answerable), "ndcgAt10": ndcg / float64(answerable), "mrrAt10": mrr / float64(answerable), "exactQueries": exact, "exactTop1Misses": exactMisses, "unrelatedQueries": noAnswer, "emptyUnrelatedResults": emptyNoAnswer, "firstQueryMs": float64(cold.Elapsed.Microseconds()) / 1000, "warmSamples": len(latencies), "warmP50Ms": latencies[len(latencies)/2], "warmP95Ms": latencies[(len(latencies)*95-1)/100], "cases": cases})
	}
	coverage, err := db.Indexing().Progress(ctx, "a", search.Profile)
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"lexicalScoringVersion": retrieval.LexicalScoringVersion, "indexedChunks": coverage.Chunks, "fixtureVersion": fixture.Version, "split": "held-out", "messages": len(fixture.Messages), "accountMessages": len(fixture.Messages) - 1, "queries": len(fixture.Queries), "rrfConstant": 60, "candidateEmailsPerBranch": 100, "modes": reports}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("SEARCH_QUALIFICATION", string(data))
}
