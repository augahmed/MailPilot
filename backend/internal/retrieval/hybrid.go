package retrieval

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

const LexicalScoringVersion = "passage-local-tf-v1"

const candidateLimit = 100
const fusionConstant = 60.0

func ValidMode(mode string) bool {
	return mode == "" || mode == "keyword" || mode == "semantic" || mode == "hybrid"
}

// LiteralQuery treats all FTS metacharacters as text. Full literal phrases and
// embedded addresses/alphanumeric codes are separately checked at boundaries.
func LiteralQuery(q string) repository.KeywordQuery {
	q = strings.TrimSpace(q)
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	terms := []string{}
	wordsForRank := []string{}
	seen := map[string]bool{}
	for _, word := range words {
		word = strings.ToLower(word)
		if !seen[word] {
			seen[word] = true
			terms = append(terms, `"`+word+`"`)
			wordsForRank = append(wordsForRank, word)
		}
	}
	literals := []string{strings.Trim(q, `"`)}
	for _, word := range strings.Fields(q) {
		word = strings.Trim(word, `"'(),;:!?`)
		letter, digit := false, false
		for _, r := range word {
			letter = letter || unicode.IsLetter(r)
			digit = digit || unicode.IsDigit(r)
		}
		if strings.Contains(word, "@") || letter && digit || digit && len(word) >= 4 {
			if len(literals) < 128 {
				literals = append(literals, word)
			}
		}
	}
	// Quoted phrases support company names inside a longer natural query.
	parts := strings.Split(q, `"`)
	for i := 1; i < len(parts); i += 2 {
		if len(literals) < 128 && strings.TrimSpace(parts[i]) != "" {
			literals = append(literals, parts[i])
		}
	}
	return repository.KeywordQuery{Match: strings.Join(terms, " OR "), Terms: wordsForRank, Literals: literals}
}

func literalMatch(text, literal string) bool {
	text, literal = strings.ToLower(text), strings.ToLower(literal)
	if literal == "" {
		return false
	}
	for offset := 0; offset <= len(text)-len(literal); {
		i := strings.Index(text[offset:], literal)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(literal)
		// Address/code punctuation is part of identity, avoiding prefix matches.
		identity := func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("@_+-", r)
		}
		left, right := true, true
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			left = !identity(r)
		}
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = !identity(r)
			if r == '.' && strings.Contains(literal, "@") && end+1 < len(text) {
				next, _ := utf8.DecodeRuneInString(text[end+1:])
				right = !identity(next)
			}
		}
		if left && right {
			return true
		}
		offset = i + len(literal)
	}
	return false
}

func exactSource(v repository.VectorSource, query repository.KeywordQuery) bool {
	for _, literal := range query.Literals {
		for _, text := range []string{v.Chunk.Text, v.Chunk.ContextText, v.Subject, v.SenderName, v.SenderAddress} {
			if literalMatch(text, literal) {
				return true
			}
		}
	}
	return false
}

func (s *VectorSearch) searchKeyword(ctx context.Context, a domain.AccountID, q string, k int, f repository.SearchFilter) (Result, error) {
	if s.Keywords == nil {
		return Result{}, embedding.ErrUnavailable
	}
	query := LiteralQuery(q)
	result, err := rank(ctx, a, k, func(visit func(hit) error) error {
		return s.Keywords.ScanKeywords(ctx, a, s.Profile, f, query, func(v repository.VectorSource, _ float64) error {
			return visit(hit{source: v, score: lexicalScore(v, query.Terms), exact: exactSource(v, query), excerpt: keywordExcerpt(v.Chunk.Text, q)})
		})
	})
	if err != nil {
		return Result{}, err
	}
	for i := range result.Emails {
		result.Emails[i].KeywordRank = i + 1
	}
	return result, s.CheckSources(ctx, a, f, result.Sources)
}

// SearchMode uses equal-weight reciprocal rank fusion at email level. Lexical and
// cosine retain their meanings and are never added together. Exact literal hits
// form a priority tier in keyword/hybrid modes; semantic-only preserves cosine.
// Empty mode preserves the existing semantic API for older callers.
func (s *VectorSearch) SearchMode(ctx context.Context, a domain.AccountID, q string, k int, f repository.SearchFilter, mode string) (Result, error) {
	start := time.Now()
	if a == "" || !ValidMode(mode) || !ValidQuery(q, k, f) {
		return Result{}, ErrInput
	}
	if mode == "" || mode == "semantic" {
		return s.Search(ctx, a, q, k, f)
	}
	depth := k
	if mode == "hybrid" {
		depth = candidateLimit
	}
	keyword, err := s.searchKeyword(ctx, a, q, depth, f)
	if err != nil {
		return Result{}, err
	}
	result := keyword
	result.Mode = "keyword"
	if mode == "hybrid" {
		semantic, err := s.searchVector(ctx, a, q, candidateLimit, f)
		if err != nil {
			if !errors.Is(err, embedding.ErrUnavailable) && !errors.Is(err, embedding.ErrClosed) && !errors.Is(err, embedding.ErrInference) && !errors.Is(err, embedding.ErrVector) && !errors.Is(err, embedding.ErrInput) {
				return Result{}, err
			}
			result.Warning = "semantic_unavailable"
		} else {
			result = fuse(keyword, semantic, k)
			result.Mode = "hybrid"
		}
	}
	if len(result.Emails) > k {
		result.Emails = result.Emails[:k]
	}
	// Retain branch evidence only for returned emails, including passages whose
	// ranks contributed even when overlap suppression hid their excerpt.
	keep := map[string]bool{}
	for _, e := range result.Emails {
		keep[e.EmailID] = true
	}
	sources := result.Sources
	result.Sources = nil
	for _, v := range sources {
		if keep[string(v.Chunk.EmailID)] {
			result.Sources = append(result.Sources, v)
		}
	}
	if err = s.CheckSources(ctx, a, f, result.Sources); err != nil {
		return Result{}, err
	}
	result.Elapsed = time.Since(start)
	return result, nil
}

func fuse(keyword, semantic Result, k int) Result {
	result := Result{Emails: []EmailResult{}, Scanned: keyword.Scanned + semantic.Scanned}
	byID := map[string]int{}
	passages := map[string]repository.EmailChunk{}
	for _, branch := range []Result{keyword, semantic} {
		for _, source := range branch.Sources {
			passages[source.Chunk.ID] = source.Chunk
		}
	}
	for _, branch := range []Result{keyword, semantic} {
		for _, email := range branch.Emails {
			at, ok := byID[email.EmailID]
			if !ok {
				at = len(result.Emails)
				byID[email.EmailID] = at
				result.Emails = append(result.Emails, email)
			} else {
				e := &result.Emails[at]
				e.SemanticRank = email.SemanticRank
				// Retain keyword evidence first, then distinct nonoverlapping semantic passages.
				for _, ev := range email.Evidence {
					duplicate := false
					for _, prior := range e.Evidence {
						if overlaps(passages[prior.ChunkID], passages[ev.ChunkID]) {
							duplicate = true
						}
					}
					if !duplicate && len(e.Evidence) < MaxEvidence {
						e.Evidence = append(e.Evidence, ev)
					}
				}
			}
		}
	}
	for i := range result.Emails {
		e := &result.Emails[i]
		e.RankScore = 0
		if e.KeywordRank > 0 {
			e.RankScore += 1 / (fusionConstant + float64(e.KeywordRank))
		}
		if e.SemanticRank > 0 {
			e.RankScore += 1 / (fusionConstant + float64(e.SemanticRank))
		}
	}
	sort.Slice(result.Emails, func(i, j int) bool {
		a, b := result.Emails[i], result.Emails[j]
		if a.ExactMatch != b.ExactMatch {
			return a.ExactMatch
		}
		if a.RankScore != b.RankScore {
			return a.RankScore > b.RankScore
		}
		return a.EmailID < b.EmailID
	})
	if len(result.Emails) > k {
		result.Emails = result.Emails[:k]
	}
	result.Sources = append(keyword.Sources, semantic.Sources...)
	return result
}

// CheckSources applies each branch's independent compatibility gate. A keyword
// hit remains valid without vectors; vector evidence still requires ready vectors.
func (s *VectorSearch) CheckSources(ctx context.Context, a domain.AccountID, f repository.SearchFilter, sources []repository.VectorSource) error {
	keyword, semantic := []repository.VectorSource{}, []repository.VectorSource{}
	for _, v := range sources {
		if v.Keyword {
			keyword = append(keyword, v)
		} else {
			semantic = append(semantic, v)
		}
	}
	if len(keyword) > 0 {
		if s.Keywords == nil {
			return repository.ErrConflict
		}
		if err := s.Keywords.CheckKeywordSources(ctx, a, s.Profile, f, keyword); err != nil {
			return err
		}
	}
	if len(semantic) > 0 {
		return s.Vectors.CheckVectorSources(ctx, a, s.Profile, f, semantic)
	}
	return ctx.Err()
}

// Center bounded keyword evidence on the first literal (then token) occurrence.
// Case folding with changed byte lengths falls back to an unmodified UTF-8 prefix.
func keywordExcerpt(text, q string) string {
	if len(text) <= 600 {
		return text
	}
	lower := strings.ToLower(text)
	if len(lower) != len(text) {
		return clip(text, 600)
	}
	terms := append(LiteralQuery(q).Literals, strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })...)
	for _, term := range terms {
		i := strings.Index(lower, strings.ToLower(term))
		if term == "" || i < 0 {
			continue
		}
		start := max(0, i-100)
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		return clip(text[start:], 600)
	}
	return clip(text, 600)
}

// lexicalScore uses per-passage term coverage and saturated frequency with a
// fixed length reference. It uses no corpus or cross-account statistics. FTS5
// still supplies token candidates; exact literal matches remain a priority tier.
func lexicalScore(v repository.VectorSource, terms []string) float64 {
	score := 0.0
	for i, text := range []string{v.Chunk.Text, v.Chunk.ContextText} {
		words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		counts := map[string]int{}
		for _, word := range words {
			counts[word]++
		}
		weight := 1.0
		if i == 1 {
			weight = .5
		}
		for _, term := range terms {
			if n := counts[term]; n > 0 {
				score += weight * float64(n) / (float64(n) + 1.2*(.25+.75*float64(len(words))/128))
			}
		}
	}
	return score
}
