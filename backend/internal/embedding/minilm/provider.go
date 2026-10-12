// Package minilm isolates all-MiniLM-L6-v2's local ONNX adapter. The fixed,
// embedded Python worker performs inference only; Go owns caching and policy.
package minilm

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mailpilot.local/core/internal/embedding"
)

const Revision = "10dbd2f06a8baf40ad285b037edda610c1b9a57c"
const ModelSHA256 = "6fd5d72fe4589f189f8ebc006442dbb529bb7ce38f8082112682524616046452"

// TokenizerSHA256 is pinned alongside the model; setup verifies both artifacts.
const TokenizerSHA256 = "be50c3628f2bf5bb5e3a7f17b1f74611b2561a3a27eeab05e5aa30f411572037"

//go:embed worker.py
var worker string

type Provider struct {
	python, modelDir string
	run              runner
}
type runner func(context.Context, []string, []byte) ([]byte, error)

var _ embedding.EmbeddingProvider = (*Provider)(nil)

func New(ctx context.Context, python, modelDir string) (*Provider, error) {
	if !filepath.IsAbs(python) || !filepath.IsAbs(modelDir) {
		return nil, embedding.ErrInput
	}
	info, err := os.Stat(python)
	if err != nil || info.IsDir() {
		return nil, embedding.ErrUnavailable
	}
	for file, hash := range map[string]string{"model.onnx": ModelSHA256, "tokenizer.json": TokenizerSHA256} {
		if err := verify(ctx, filepath.Join(modelDir, file), hash); err != nil {
			return nil, err
		}
	}
	return &Provider{python: python, modelDir: modelDir, run: runWorker}, nil
}
func verify(ctx context.Context, path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return embedding.ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 100*1024*1024 {
		return embedding.ErrUnavailable
	}
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return embedding.ErrUnavailable
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return embedding.ErrUnavailable
	}
	return nil
}
func (p *Provider) Dimension() int { return 384 }
func (p *Provider) ProviderName() string {
	return "local/all-MiniLM-L6-v2/" + Revision + "/onnx-mean-l2-256-v1"
}
func (p *Provider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	v, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > embedding.MaxBatchSize {
		return nil, embedding.ErrInput
	}
	for _, text := range texts {
		if len(text) > embedding.MaxTextBytes || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
			return nil, embedding.ErrInput
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, _ := json.Marshal(struct {
		Texts []string `json:"texts"`
	}{texts})
	data, err := p.run(ctx, []string{p.python, "-I", "-u", "-c", worker, p.modelDir}, request)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, embedding.ErrProvider
	}
	var response struct {
		Vectors [][]float32 `json:"vectors"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || len(response.Vectors) != len(texts) {
		return nil, embedding.ErrVector
	}
	for _, v := range response.Vectors {
		if embedding.ValidateVector(v, p.Dimension()) != nil {
			return nil, embedding.ErrVector
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		norm = math.Sqrt(norm)
		for i := range v {
			v[i] = float32(float64(v[i]) / norm)
		}
	}
	return response.Vectors, nil
}

type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, embedding.ErrVector
	}
	return b.Buffer.Write(p)
}
func runWorker(ctx context.Context, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard
	var output cappedOutput
	cmd.Stdout = &output
	cmd.WaitDelay = time.Second
	// A minimal environment avoids forwarding credential-bearing parent values.
	cmd.Env = []string{"ORT_DISABLE_TELEMETRY=1", "HF_HUB_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "TOKENIZERS_PARALLELISM=false"}
	if err := cmd.Run(); err != nil {
		return nil, embedding.ErrProvider
	}
	return output.Bytes(), nil
}
