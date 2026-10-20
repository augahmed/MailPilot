//go:build darwin && arm64 && cgo && index_native

package app

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

func TestNativeVectorSearchEvaluation(t *testing.T) {
	raw, err := os.ReadFile("../retrieval/testdata/evaluation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Messages []struct{ ID, Account, Subject, Body, Thread string }
		Queries  []struct {
			Kind, Query string
			Relevant    []string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	initial, db, _, _, _ := indexFixture(t, 0)
	_ = initial.Close(context.Background())
	ctx := context.Background()
	for _, m := range fixture.Messages {
		e := domain.Email{ID: domain.EmailID(m.ID), AccountID: domain.AccountID(m.Account), ProviderID: domain.ProviderMessageID(m.ID), ProviderThreadID: domain.ProviderThreadID(m.Thread), Subject: m.Subject, BodyText: m.Body, BodyState: "complete", Sender: domain.EmailAddress{Name: "Synthetic sender", Address: "fixture@example.test"}, ReceivedAt: time.Unix(1700000000, 0)}
		if _, err = db.Emails().Upsert(ctx, e); err != nil {
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
	other, err := NewLocalIndex(ctx, func() (domain.AccountID, context.Context, error) { return "b", ctx, nil }, db.Emails(), db.Chunks(), db.Indexing(), provider)
	if err != nil {
		t.Fatal(err)
	}
	other.Manage(ctx, IndexRequest{Operation: "start", AccountID: "b"})
	deadline := time.Now().Add(30 * time.Second)
	for {
		r := other.Manage(ctx, IndexRequest{Operation: "status", AccountID: "b"})
		if r.Status != "running" {
			if r.Status != "complete" {
				t.Fatal(r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index timeout")
		}
		time.Sleep(time.Millisecond)
	}
	if err = other.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = idx.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Reopen the same provider type for cold local model/query initialization timing.
	queryProvider, err := embedding.New()
	if err != nil {
		t.Fatal(err)
	}
	defer queryProvider.Close(ctx)
	search := retrieval.New(db.Vectors(), queryProvider)
	cold, err := search.Search(ctx, "a", fixture.Queries[0].Query, 10, repository.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []map[string]any{}
	latencies := []float64{}
	sum5, sum10 := 0.0, 0.0
	answerable := 0
	for _, q := range fixture.Queries {
		relevant := map[string]bool{}
		for _, id := range q.Relevant {
			relevant[id] = true
		}
		var result retrieval.Result
		for repeat := 0; repeat < 5; repeat++ {
			result, err = search.Search(ctx, "a", q.Query, 10, repository.SearchFilter{})
			if err != nil {
				t.Fatal(err)
			}
			latencies = append(latencies, float64(result.Elapsed.Microseconds())/1000)
		}
		hits5, hits10 := 0, 0
		ids := []string{}
		for rank, e := range result.Emails {
			ids = append(ids, e.EmailID)
			if e.EmailID == "flight-private" {
				t.Fatal("cross-account leak")
			}
			if relevant[e.EmailID] {
				hits10++
				if rank < 5 {
					hits5++
				}
			}
		}
		row := map[string]any{"kind": q.Kind, "query": q.Query, "relevant": q.Relevant, "top10": ids, "latencyMs": float64(result.Elapsed.Microseconds()) / 1000}
		if len(relevant) > 0 {
			r5, r10 := float64(hits5)/float64(len(relevant)), float64(hits10)/float64(len(relevant))
			sum5 += r5
			sum10 += r10
			answerable++
			row["recallAt5"] = r5
			row["recallAt10"] = r10
		} else {
			row["topScore"] = result.Emails[0].Evidence[0].Score
			row["recallAt5"] = nil
			row["recallAt10"] = nil
		}
		cases = append(cases, row)
	}
	sort.Float64s(latencies)
	report := map[string]any{"fixtureVersion": 1, "messages": len(fixture.Messages), "searchableAccountMessages": len(fixture.Messages) - 1, "chunks": cold.Scanned, "answerableQueries": answerable, "unrelatedQueries": len(fixture.Queries) - answerable, "recallAt5": sum5 / float64(answerable), "recallAt10": sum10 / float64(answerable), "coldQueryMs": float64(cold.Elapsed.Microseconds()) / 1000, "warmSamples": len(latencies), "warmP50Ms": latencies[len(latencies)/2], "warmP95Ms": latencies[(len(latencies)*95-1)/100], "cases": cases}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("SEARCH_QUALIFICATION", string(data))
}
