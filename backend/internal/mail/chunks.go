package mail

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"mailpilot.local/core/internal/embedding/model"
	"mailpilot.local/core/internal/mail/wordpiece"
	"mailpilot.local/core/internal/repository"
)

const (
	ChunkTokens          = 256
	ChunkOverlap         = 32
	PreprocessingVersion = "email-sections-" + TextProjectionVersion + "-wp-07eced375cec-256-32-" + wordpiece.NormalizationVersion
	ChunkIndexVersion    = "email-chunks-v1"
	maxChunkTextBytes    = 64 * 1024
)

type Chunker struct{ tokenizer *wordpiece.Tokenizer }

func NewChunker() (*Chunker, error) {
	t, err := wordpiece.New()
	if err != nil {
		return nil, err
	}
	return &Chunker{t}, nil
}
func ChunkModel() repository.EmbeddingModel {
	return repository.EmbeddingModel{ID: wordpiece.ModelID, Version: model.Version, Dimension: model.Dimensions}
}

// ModelInput is the one canonical document recipe for future inference. Subject
// context is bounded separately and never changes the exact source passage.
func ModelInput(c repository.ChunkInput) string { return c.ContextText + c.Text }

func (c *Chunker) Build(ctx context.Context, stored repository.StoredEmail) (repository.ChunkSnapshot, error) {
	e := stored.Email
	if e.AccountID == "" || e.ID == "" || len(stored.ContentHash) != 64 {
		return repository.ChunkSnapshot{}, repository.ErrInvalid
	}
	projection, err := Sections(ctx, e)
	if err != nil {
		return repository.ChunkSnapshot{}, err
	}
	result := repository.ChunkSnapshot{AccountID: e.AccountID, EmailID: e.ID, SourceRevision: stored.ContentHash, PreprocessingVersion: PreprocessingVersion, IndexVersion: ChunkIndexVersion, Model: ChunkModel(), ContentStatus: projection.ContentStatus}
	subject, err := c.subjectContext(ctx, projection.Sources["subject"])
	if err != nil {
		return repository.ChunkSnapshot{}, err
	}
	bodyChunks, totalBytes := 0, 0
	for _, s := range projection.Sections {
		if err := ctx.Err(); err != nil {
			return repository.ChunkSnapshot{}, err
		}
		prefix := "Section: " + s.Kind + "\n"
		if s.Kind != "subject" {
			prefix = subject + prefix
		}
		prefixTokens, err := c.tokenizer.Tokens(ctx, prefix)
		if err != nil {
			return repository.ChunkSnapshot{}, err
		}
		budget := ChunkTokens - 2 - len(prefixTokens) // Reserve CLS/SEP; never silently truncate.
		tokens, err := c.tokenizer.Tokens(ctx, s.Text)
		if err != nil {
			return repository.ChunkSnapshot{}, err
		}
		for start := 0; start < len(tokens); {
			if err := ctx.Err(); err != nil {
				return repository.ChunkSnapshot{}, err
			}
			end := min(start+budget, len(tokens))
			if end < len(tokens) {
				for end > start && !tokens[end-1].WordEnd {
					end--
				}
				end = preferredEnd(s.Text, tokens, start, end)
			}
			if end <= start {
				return repository.ChunkSnapshot{}, ErrContent
			}
			first := tokens[start].Start
			if start == 0 {
				first = 0
			}
			last := len(s.Text)
			if end < len(tokens) {
				last = tokens[end].Start
			}
			text := s.Text[first:last]
			if len(text) > maxChunkTextBytes {
				return repository.ChunkSnapshot{}, ErrContent
			}
			exact, err := c.tokenizer.Tokens(ctx, prefix+text)
			if err != nil {
				return repository.ChunkSnapshot{}, err
			}
			if len(exact)+2 > ChunkTokens {
				return repository.ChunkSnapshot{}, ErrContent
			}
			sum := sha256.Sum256([]byte(text))
			result.Chunks = append(result.Chunks, repository.ChunkInput{Index: len(result.Chunks), SectionType: s.Kind, Text: text, TokenCount: len(exact) + 2, StartOffset: s.Start + first, EndOffset: s.Start + last, ContentHash: fmt.Sprintf("%x", sum), SourceKind: s.Source, SectionIndex: s.Index, SectionStartOffset: s.Start, SectionEndOffset: s.End, ContextText: prefix})
			totalBytes += len(text)
			if len(result.Chunks) > MaxSections || totalBytes > 4*1024*1024 {
				return repository.ChunkSnapshot{}, ErrContent
			}
			if s.Kind != "subject" {
				bodyChunks++
			}
			if end == len(tokens) {
				break
			}
			next := max(start+1, end-ChunkOverlap)
			// Extend overlap to a complete word, while always advancing.
			for next > start+1 && !tokens[next].WordStart {
				next--
			}
			for next < len(tokens) && !tokens[next].WordStart {
				next++
			}
			if next >= end {
				next = end
			}
			start = next
		}
	}
	if bodyChunks == 0 {
		result.ContentStatus = "unavailable"
	}
	return result, nil
}

func (c *Chunker) subjectContext(ctx context.Context, subject string) (string, error) {
	// All subject text gets its own source chunks; only this repeated context is
	// capped. Never embed transport/authentication headers or long recipient lists.
	subject = strings.Join(strings.Fields(subject), " ")
	if subject == "" {
		return "", nil
	}
	tokens, err := c.tokenizer.Tokens(ctx, subject)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", nil
	}
	end := min(22, len(tokens))
	for end > 0 && (!tokens[end-1].WordEnd || tokens[end-1].End > 256) {
		end--
	}
	if end == 0 {
		return "", nil
	}
	return "Subject: " + subject[:tokens[end-1].End] + "\n", nil
}
func preferredEnd(text string, tokens []wordpiece.Token, start, end int) int {
	if end == len(tokens) {
		return end
	}
	paragraph, sentence := 0, 0
	minimum := max(start+ChunkOverlap+1, start+(end-start)*3/5)
	for i := minimum; i <= end; i++ {
		if !tokens[i-1].WordEnd {
			continue
		}
		gap := text[tokens[i-1].End:tokens[i].Start]
		if strings.Contains(gap, "\n\n") {
			paragraph = i
		}
		before := text[:tokens[i-1].End]
		if strings.ContainsAny(gap, " \t\r\n") && strings.ContainsAny(before[len(before)-1:], ".!?") {
			sentence = i
		}
	}
	if paragraph > 0 {
		return paragraph
	}
	if sentence > 0 {
		return sentence
	}
	return end
}
