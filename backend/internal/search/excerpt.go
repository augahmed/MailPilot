package search

import (
	"sort"
	"strings"
	"unicode"
)

func excerpts(prepared, query string) []string {
	_, body, ok := strings.Cut(prepared, "\nBody:\n")
	if !ok {
		return nil
	}
	lines := strings.FieldsFunc(body, func(r rune) bool { return r == '\n' || r == '.' || r == '!' || r == '?' })
	terms := map[string]bool{}
	for _, term := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(term) > 2 {
			terms[term] = true
		}
	}
	type passage struct {
		text  string
		score int
	}
	var candidates []passage
	seen := map[string]bool{}
	for _, line := range lines {
		line = clipDisplay(line, 160)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		score := 0
		for _, word := range strings.FieldsFunc(strings.ToLower(line), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			if terms[word] {
				score++
			}
		}
		candidates = append(candidates, passage{line, score})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	result := []string{}
	for _, candidate := range candidates[:min(len(candidates), 8)] {
		result = append(result, candidate.text)
	}
	return result
}
