package minilm

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"mailpilot.local/core/internal/embedding"
)

func TestProviderPrivateProtocolAndNormalization(t *testing.T) {
	p := &Provider{python: "/trusted/python", modelDir: "/trusted/model"}
	p.run = func(ctx context.Context, args []string, input []byte) ([]byte, error) {
		if args[0] != p.python || args[1] != "-I" || args[4] != worker || args[5] != p.modelDir {
			t.Fatal("unexpected worker command")
		}
		var request struct {
			Texts []string `json:"texts"`
		}
		if json.Unmarshal(input, &request) != nil || len(request.Texts) != 2 {
			t.Fatal("invalid private request")
		}
		vector := make([]float32, 384)
		vector[0] = 3
		vector[1] = 4
		return json.Marshal(map[string]any{"vectors": [][]float32{vector, vector}})
	}
	v, err := p.EmbedBatch(context.Background(), []string{"Synthetic first", "Synthetic second"})
	if err != nil || len(v) != 2 || math.Abs(float64(v[0][0])-.6) > 1e-6 {
		t.Fatal(err)
	}
}
func TestProviderRejectsFailuresWithoutMailLeakage(t *testing.T) {
	for _, data := range []string{`{"vectors":[]}`, `{"vectors":[[1]]}`, `{"vectors":[],"secret":"SYNTHETIC"}`, `{} {}`} {
		p := &Provider{run: func(context.Context, []string, []byte) ([]byte, error) { return []byte(data), nil }}
		if _, err := p.EmbedText(context.Background(), "Synthetic"); err != embedding.ErrVector {
			t.Fatal(err)
		}
	}
	p := &Provider{run: func(context.Context, []string, []byte) ([]byte, error) {
		return nil, errors.New("SYNTHETIC private data")
	}}
	if _, err := p.EmbedText(context.Background(), "Synthetic"); err != embedding.ErrProvider {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.EmbedText(ctx, "Synthetic"); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := p.EmbedText(context.Background(), strings.Repeat("x", embedding.MaxTextBytes+1)); err != embedding.ErrInput {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), "relative", "relative"); err != embedding.ErrInput {
		t.Fatal(err)
	}
}
func TestOptInLocalModel(t *testing.T) {
	python, model := os.Getenv("MAILPILOT_TEST_EMBEDDING_PYTHON"), os.Getenv("MAILPILOT_TEST_EMBEDDING_MODEL_DIR")
	if python == "" || model == "" {
		t.Skip("local model smoke test requires explicit test paths")
	}
	p, err := New(context.Background(), python, model)
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.EmbedBatch(context.Background(), []string{"Synthetic purchase receipt", "Synthetic purchase receipt", "A family lunch invitation"})
	if err != nil {
		t.Fatal(err)
	}
	var norm, dot, other float64
	for i, x := range v[0] {
		norm += float64(x) * float64(x)
		dot += float64(x) * float64(v[1][i])
		other += float64(x) * float64(v[2][i])
	}
	if math.Abs(norm-1) > 1e-5 || dot < .999 || other >= dot {
		t.Fatal("invalid model embedding")
	}
}
