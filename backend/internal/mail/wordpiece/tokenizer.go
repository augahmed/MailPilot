// Package wordpiece implements the pinned MiniLM BERT tokenizer locally, without
// inference, downloads, truncation or a native runtime dependency.
package wordpiece

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	ModelID           = "sentence-transformers/all-MiniLM-L6-v2"
	Revision          = "1110a243fdf4706b3f48f1d95db1a4f5529b4d41"
	VocabSHA256       = "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3"
	MaxSequenceTokens = 256
	MaxTextBytes      = 1024 * 1024
	MaxTokens         = 262144
	// Unicode table changes can alter token IDs even with the same vocabulary.
	NormalizationVersion = "u" + unicode.Version + "-nfd" + norm.Version
)

var ErrText = errors.New("unsupported or oversized tokenizer input")

//go:embed assets/*
var assets embed.FS

type Token struct {
	ID                 int
	Start, End         int // UTF-8 byte offsets in the unmodified supplied text.
	WordStart, WordEnd bool
}
type Tokenizer struct{ vocab map[string]int }

func New() (*Tokenizer, error) {
	data, err := assets.ReadFile("assets/vocab.txt")
	if err != nil {
		return nil, ErrText
	}
	vocabulary := string(data)
	if fmt.Sprintf("%x", sha256.Sum256([]byte(vocabulary))) != VocabSHA256 {
		return nil, ErrText
	}
	words := strings.Split(strings.TrimSuffix(vocabulary, "\n"), "\n")
	if len(words) != 30522 {
		return nil, ErrText
	}
	vocab := make(map[string]int, len(words))
	for i, w := range words {
		vocab[w] = i
	}
	return &Tokenizer{vocab: vocab}, nil
}

type letter struct {
	value      rune
	start, end int
}

var specials = []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]"}

// Tokens applies BertNormalizer(clean_text, Chinese splitting, lowercase, NFD
// accent stripping), BertPreTokenizer and greedy WordPiece, max word length 100.
// Added special tokens are matched before normalization, as in tokenizer.json.
func (t *Tokenizer) Tokens(ctx context.Context, text string) ([]Token, error) {
	if len(text) > MaxTextBytes || !utf8.ValidString(text) {
		return nil, ErrText
	}
	result := make([]Token, 0, min(len(text)/3, MaxTokens))
	word := []letter{}
	flush := func() {
		if len(word) == 0 {
			return
		}
		beginning := len(result)
		if len(word) > 100 {
			result = append(result, Token{ID: 100, Start: word[0].start, End: word[len(word)-1].end})
		} else {
			start := 0
			for start < len(word) {
				found := -1
				finish := start
				for end := len(word); end > start; end-- {
					var b strings.Builder
					if start > 0 {
						b.WriteString("##")
					}
					for _, c := range word[start:end] {
						b.WriteRune(c.value)
					}
					if id, ok := t.vocab[b.String()]; ok {
						found = id
						finish = end
						break
					}
				}
				if found < 0 {
					result = result[:beginning]
					result = append(result, Token{ID: 100, Start: word[0].start, End: word[len(word)-1].end})
					break
				}
				result = append(result, Token{ID: found, Start: word[start].start, End: word[finish-1].end})
				start = finish
			}
		}
		result[beginning].WordStart = true
		result[len(result)-1].WordEnd = true
		word = word[:0]
	}
	for pos, steps := 0, 0; pos < len(text); steps++ {
		if steps%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if len(result) > MaxTokens {
			return nil, ErrText
		}
		matched := false
		if text[pos] == '[' {
			for _, s := range specials {
				if strings.HasPrefix(text[pos:], s) {
					flush()
					result = append(result, Token{ID: t.vocab[s], Start: pos, End: pos + len(s), WordStart: true, WordEnd: true})
					pos += len(s)
					matched = true
					break
				}
			}
		}
		if matched {
			continue
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		start := pos
		pos += size
		if r == 0 || r == utf8.RuneError || (r != '\t' && r != '\n' && r != '\r' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))) {
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		punct := isPunctuation(r) || isChinese(r)
		if punct {
			flush()
		}
		// NFD first also handles capital dotted I when lowercasing/stripping accents.
		for _, c := range norm.NFD.String(string(r)) {
			if !unicode.Is(unicode.Mn, c) {
				if len(word) <= 100 {
					word = append(word, letter{unicode.ToLower(c), start, pos})
				} else {
					word[len(word)-1].end = pos
				}
			}
		}
		if punct {
			flush()
		}
	}
	flush()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(result) > MaxTokens {
		return nil, ErrText
	}
	return result, nil
}
func isPunctuation(r rune) bool {
	return r >= 33 && r <= 47 || r >= 58 && r <= 64 || r >= 91 && r <= 96 || r >= 123 && r <= 126 || unicode.IsPunct(r)
}
func isChinese(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff || r >= 0x3400 && r <= 0x4dbf || r >= 0x20000 && r <= 0x2a6df || r >= 0x2a700 && r <= 0x2b73f || r >= 0x2b740 && r <= 0x2b81f || r >= 0x2b820 && r <= 0x2ceaf || r >= 0xf900 && r <= 0xfaff || r >= 0x2f800 && r <= 0x2fa1f
}
