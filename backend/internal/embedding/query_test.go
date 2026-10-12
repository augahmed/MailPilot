package embedding

import (
	"context"
	"testing"
)

type reusedProvider struct{ buffer []float32 }

func (p *reusedProvider) ProviderName() string                                 { return "fixture/reused" }
func (p *reusedProvider) Dimension() int                                       { return 2 }
func (p *reusedProvider) EmbedText(context.Context, string) ([]float32, error) { return p.buffer, nil }
func (p *reusedProvider) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return [][]float32{p.buffer}, nil
}
func TestQueryEmbeddingDoesNotAliasProviderBuffer(t *testing.T) {
	p := &reusedProvider{buffer: []float32{1, 0}}
	s := &Service{provider: p, name: p.ProviderName(), dimension: 2, gate: make(chan struct{}, 1)}
	query, err := s.EmbedTexts(context.Background(), []string{"query"})
	if err != nil {
		t.Fatal(err)
	}
	p.buffer[0] = 0
	p.buffer[1] = 1
	passage, err := s.EmbedTexts(context.Background(), []string{"passage"})
	if err != nil {
		t.Fatal(err)
	}
	if query[0][0] != 1 || query[0][1] != 0 || passage[0][1] != 1 {
		t.Fatal("query vector changed during later inference")
	}
}
