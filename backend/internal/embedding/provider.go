// Package embedding owns one bounded local embedding provider. It has no SQLite,
// mailbox, credentials, cloud inference, model download or executable interface.
package embedding

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"mailpilot.local/core/internal/embedding/model"
	"mailpilot.local/core/internal/mail/wordpiece"
)

const (
	MaxBatch       = 4
	MaxInputBytes  = 64*1024 + 1024
	MaxRequestTime = 30 * time.Second
)

var (
	ErrInput       = errors.New("invalid embedding input")
	ErrUnavailable = errors.New("local embedding resources unavailable")
	ErrInference   = errors.New("local embedding inference failed")
	ErrVector      = errors.New("invalid local embedding output")
	ErrClosed      = errors.New("local embedding provider closed")
)

type tokenBatch struct {
	IDs, Mask, Types []int64
	Rows, Length     int
}
type engine interface {
	Run(context.Context, tokenBatch) ([]float32, error)
	Close() error
}
type engineFactory func(context.Context) (engine, error)

// Provider lazily initializes one session, serializes bounded batches, and revokes
// active/waiting calls at Close. Factories are private and injectable only in tests.
type Provider struct {
	tokenizer             *wordpiece.Tokenizer
	factory               engineFactory
	lifetime              context.Context
	stop                  context.CancelFunc
	gate                  chan struct{}
	mu                    sync.Mutex
	started, closed       bool
	initialized, finished chan struct{}
	session               engine
	initErr, closeErr     error
}

func New() (*Provider, error) { return newProvider(loadNative) }
func newProvider(factory engineFactory) (*Provider, error) {
	tokenizer, err := wordpiece.New()
	if err != nil {
		return nil, ErrUnavailable
	}
	lifetime, stop := context.WithCancel(context.Background())
	return &Provider{tokenizer: tokenizer, factory: factory, lifetime: lifetime, stop: stop, gate: make(chan struct{}, 1), initialized: make(chan struct{}), finished: make(chan struct{})}, nil
}
func (p *Provider) Descriptor() model.Descriptor { return model.Identity() }

func (p *Provider) initialize() {
	ctx, cancel := context.WithTimeout(p.lifetime, MaxRequestTime)
	defer cancel()
	session, err := p.factory(ctx)
	if err == nil && session == nil {
		err = ErrUnavailable
	}
	if ctx.Err() != nil && err == nil {
		err = ctx.Err()
	}
	if err != nil && session != nil {
		_ = session.Close()
		session = nil
	}
	p.mu.Lock()
	p.session = session
	p.initErr = err
	p.mu.Unlock()
	close(p.initialized)
}
func (p *Provider) ready(ctx context.Context) (engine, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	if !p.started {
		p.started = true
		go p.initialize()
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.initialized:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	return p.session, p.initErr
}

// EmbedDocument accepts canonical document inputs (mail.ModelInput for chunks).
// It never truncates. Fresh local vectors have the same space as EmbedQuery.
func (p *Provider) EmbedDocument(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) < 1 || len(texts) > MaxBatch {
		return nil, ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, MaxRequestTime)
	defer cancel()
	stop := context.AfterFunc(p.lifetime, cancel)
	defer stop()
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	batch, err := p.tokenize(ctx, texts)
	if err != nil {
		return nil, err
	}
	session, err := p.ready(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	closed = p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	output, err := session.Run(ctx, batch)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, ErrInference
	}
	return pool(ctx, output, batch)
}

// MiniLM is symmetric: query text uses the same tokenizer/pooling without an
// invented E5/query prefix or email-only context. Caller supplies a bounded query.
func (p *Provider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	result, err := p.EmbedDocument(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return result[0], nil
}
func (p *Provider) tokenize(ctx context.Context, texts []string) (tokenBatch, error) {
	rows := make([][]int64, len(texts))
	longest := 0
	for i, text := range texts {
		if len(text) > MaxInputBytes || strings.TrimSpace(text) == "" {
			return tokenBatch{}, ErrInput
		}
		tokens, err := p.tokenizer.Tokens(ctx, text)
		if ctx.Err() != nil {
			return tokenBatch{}, ctx.Err()
		}
		if err != nil || len(tokens) == 0 || len(tokens)+2 > wordpiece.MaxSequenceTokens {
			return tokenBatch{}, ErrInput
		}
		ids := make([]int64, len(tokens)+2)
		ids[0] = 101
		ids[len(ids)-1] = 102
		for j, t := range tokens {
			ids[j+1] = int64(t.ID)
		}
		rows[i] = ids
		longest = max(longest, len(ids))
	}
	b := tokenBatch{Rows: len(rows), Length: longest, IDs: make([]int64, len(rows)*longest), Mask: make([]int64, len(rows)*longest), Types: make([]int64, len(rows)*longest)}
	for i, ids := range rows {
		copy(b.IDs[i*longest:], ids)
		for j := range ids {
			b.Mask[i*longest+j] = 1
		}
	}
	return b, nil
}
func pool(ctx context.Context, output []float32, b tokenBatch) ([][]float32, error) {
	if b.Rows < 1 || b.Rows > MaxBatch || b.Length < 2 || b.Length > 256 || len(output) != b.Rows*b.Length*model.Dimensions {
		return nil, ErrVector
	}
	vectors := make([][]float32, b.Rows)
	for row := 0; row < b.Rows; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sums := make([]float64, model.Dimensions)
		count := 0
		for token := 0; token < b.Length; token++ {
			if b.Mask[row*b.Length+token] == 0 {
				continue
			}
			count++
			for d := range sums {
				v := float64(output[(row*b.Length+token)*model.Dimensions+d])
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, ErrVector
				}
				sums[d] += v
			}
		}
		if count == 0 {
			return nil, ErrVector
		}
		norm := float64(0)
		for d := range sums {
			sums[d] /= float64(count)
			norm += sums[d] * sums[d]
		}
		if norm == 0 || math.IsInf(norm, 0) || math.IsNaN(norm) {
			return nil, ErrVector
		}
		norm = math.Sqrt(norm)
		vector := make([]float32, model.Dimensions)
		for d := range vector {
			vector[d] = float32(sums[d] / norm)
		}
		if err := validateVector(vector); err != nil {
			return nil, err
		}
		vectors[row] = vector
	}
	return vectors, nil
}
func validateVector(v []float32) error {
	if len(v) != model.Dimensions {
		return ErrVector
	}
	norm := float64(0)
	for _, f := range v {
		x := float64(f)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrVector
		}
		norm += x * x
	}
	if math.Abs(norm-1) > 1e-4 {
		return ErrVector
	}
	return nil
}

// Close cancels waiters and native runs, then releases session/environment exactly
// once after active work stops. If ctx expires, cleanup continues; call Close again
// to join it. Native session construction has no ORT cancellation API.
func (p *Provider) Close(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.stop()
		go p.finish()
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.finished:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.closeErr
	}
}
func (p *Provider) finish() {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if started {
		<-p.initialized
	}
	p.gate <- struct{}{}
	defer func() { <-p.gate }()
	p.mu.Lock()
	session := p.session
	p.mu.Unlock()
	var err error
	if session != nil {
		err = session.Close()
	}
	p.mu.Lock()
	p.closeErr = err
	p.session = nil
	p.mu.Unlock()
	close(p.finished)
}
