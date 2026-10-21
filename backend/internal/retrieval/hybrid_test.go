package retrieval

import (
	"math"
	"testing"

	"mailpilot.local/core/internal/repository"
)

func TestFusionUsesEmailRanksAndPreservesExactTier(t *testing.T) {
	keyword := Result{Emails: []EmailResult{{EmailID: "literal", KeywordRank: 1, ExactMatch: true}, {EmailID: "both", KeywordRank: 2}, {EmailID: "keyword", KeywordRank: 3}}}
	semantic := Result{Emails: []EmailResult{{EmailID: "semantic", SemanticRank: 1}, {EmailID: "both", SemanticRank: 2}, {EmailID: "literal", SemanticRank: 3}}}
	got := fuse(keyword, semantic, 10)
	if len(got.Emails) != 4 || got.Emails[0].EmailID != "literal" || got.Emails[1].EmailID != "both" || math.Abs(got.Emails[1].RankScore-2.0/62) > 1e-10 {
		t.Fatal(got)
	}
	// Overlapping passages in different branches do not duplicate evidence.
	c := repository.EmailChunk{ID: "a", ChunkInput: repository.ChunkInput{Text: "same", SourceKind: "body_text", EndOffset: 4}}
	d := c
	d.ID = "b"
	keyword.Emails[0].Evidence = []Evidence{{ChunkID: "a"}}
	semantic.Emails[2].Evidence = []Evidence{{ChunkID: "b"}}
	keyword.Sources = []repository.VectorSource{{Chunk: c}}
	semantic.Sources = []repository.VectorSource{{Chunk: d}}
	got = fuse(keyword, semantic, 1)
	if len(got.Emails[0].Evidence) != 1 {
		t.Fatal("overlapping evidence", got)
	}
}

func TestLiteralIdentityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text, literal string
		want          bool
	}{
		{"Code Q7M4ZP.", "Q7M4ZP", true}, {"Q7M4ZP-extra", "Q7M4ZP", false},
		{"Contact a@northstar.test today", "a@northstar.test", true}, {"x-a@northstar.test", "a@northstar.test", false},
		{"Northstar Labs, Inc.", "Northstar Labs", true}, {"Northstar LabsExtra", "Northstar Labs", false},
	} {
		if got := literalMatch(tc.text, tc.literal); got != tc.want {
			t.Fatalf("%q / %q = %v", tc.text, tc.literal, got)
		}
	}
}
