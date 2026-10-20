package retrieval

import (
	"context"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding/model"
	"mailpilot.local/core/internal/repository"
	"math"
	"testing"
)

func TestCosineAndBounds(t *testing.T) {
	for _, tc := range []struct {
		a, b []float32
		want float64
	}{{[]float32{2, 0}, []float32{9, 0}, 1}, {[]float32{1, 0}, []float32{0, 2}, 0}, {[]float32{-1, 0}, []float32{2, 0}, -1}} {
		got, err := Cosine(tc.a, tc.b)
		if err != nil || math.Abs(got-tc.want) > 1e-8 {
			t.Fatal(got, err)
		}
	}
	for _, v := range [][]float32{nil, {0, 0}, {float32(math.NaN()), 1}, {float32(math.Inf(1)), 1}, {1}} {
		if _, err := Cosine([]float32{1, 0}, v); err == nil {
			t.Fatal("invalid vector accepted")
		}
	}
	for _, tc := range []struct {
		q string
		k int
		f repository.SearchFilter
	}{{"", 5, repository.SearchFilter{}}, {"a", 0, repository.SearchFilter{}}, {"a", 21, repository.SearchFilter{}}, {"a", 5, repository.SearchFilter{After: 20, Before: 10}}} {
		if ValidQuery(tc.q, tc.k, tc.f) {
			t.Fatal("invalid query")
		}
	}

}

type fixtureVectors struct {
	rows     []repository.VectorSource
	checkErr error
}

func (f fixtureVectors) ScanVectors(ctx context.Context, _ domain.AccountID, _ repository.IndexProfile, _ repository.SearchFilter, visit func(repository.VectorSource) error) error {
	for _, v := range f.rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(v); err != nil {
			return err
		}
	}
	return nil
}
func (f fixtureVectors) CheckVectorSources(context.Context, domain.AccountID, repository.IndexProfile, repository.SearchFilter, []repository.VectorSource) error {
	return f.checkErr
}

type fixtureQuery struct{}

func (fixtureQuery) Descriptor() model.Descriptor {
	return model.Descriptor{ModelID: "fixture", Version: "v1", Dimensions: 2}
}
func (fixtureQuery) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}
func TestExactChunkHeapKeepsHighestAndRejectsStale(t *testing.T) {
	rows := []repository.VectorSource{}
	for i, v := range [][]float32{{0, 1}, {.5, .5}, {1, 0}, {-1, 0}} {
		id := string(rune('a' + i))
		rows = append(rows, repository.VectorSource{Chunk: repository.EmailChunk{ID: id, AccountID: "account", EmailID: domain.EmailID(id), ChunkInput: repository.ChunkInput{Text: "passage"}}, Vector: v})
	}
	store := fixtureVectors{rows: rows}
	search := New(store, fixtureQuery{})
	top, err := search.SearchChunks(context.Background(), "account", "query", 2, repository.SearchFilter{})
	if err != nil || len(top) != 2 || top[0].Source.Chunk.ID != "c" || top[1].Source.Chunk.ID != "b" {
		t.Fatalf("incorrect top chunks: %+v %v", top, err)
	}
	store.checkErr = repository.ErrConflict
	search.Vectors = store
	if top, err = search.SearchChunks(context.Background(), "account", "query", 2, repository.SearchFilter{}); err != repository.ErrConflict || top != nil {
		t.Fatal("stale exact neighbors returned")
	}
}
