package wordpiece

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPinnedHuggingFaceParity(t *testing.T) {
	data, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Text string
		IDs  []int
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	tokenizer, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Text, func(t *testing.T) {
			got, err := tokenizer.Tokens(context.Background(), f.Text)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int, 0, len(got))
			for _, tok := range got {
				ids = append(ids, tok.ID)
				if tok.Start < 0 || tok.End > len(f.Text) || tok.End <= tok.Start || !utf8.ValidString(f.Text[tok.Start:tok.End]) {
					t.Fatal("invalid source offsets")
				}
			}
			if !reflect.DeepEqual(ids, f.IDs) {
				t.Fatalf("IDs %v; reference %v", ids, f.IDs)
			}
		})
	}
}
func TestTokenizerLimitsAndCancellation(t *testing.T) {
	tokenizer, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string([]byte{255}), strings.Repeat("a", MaxTextBytes+1), strings.Repeat("!", MaxTokens+1)} {
		if _, err = tokenizer.Tokens(context.Background(), text); !errors.Is(err, ErrText) {
			t.Fatal("unbounded input accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = tokenizer.Tokens(ctx, "synthetic"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}
