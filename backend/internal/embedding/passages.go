package embedding

import (
	"crypto/sha256"
	"fmt"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/mail"
	"strings"
	"unicode/utf8"
)

// Separate from classification preprocessing: changing retrieval must not change
// classification decisions or silently reinterpret its persisted vectors.
const RetrievalVersion = "email-passages-v1"

// A conservative UTF-8 byte budget keeps even punctuation-heavy/non-Latin input
// below MiniLM's 254 content-wordpiece budget, without approximating words as tokens.
const PassageBytes = 240
const MaxPassages = 8192

type Passage struct {
	Kind, Text, Hash string
	Ordinal          int
}
type RetrievalText struct {
	Hash     string
	Passages []Passage
}

func textHash(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(RetrievalVersion+"\x00"+value)))
}

func RetrievalBody(e domain.Email) (string, error) {
	if len(e.BodyText) > mail.MaxBodyBytes || !utf8.ValidString(e.BodyText) {
		return "", ErrInput
	}
	body := e.BodyText
	useHTML := strings.TrimSpace(body) == ""
	if e.BodyHTML != nil && !useHTML {
		// Gmail normalization caches extracted HTML text as BodyText. Recover
		// structural quote boundaries only when that derivation matches exactly.
		extracted, err := mail.HTMLText(*e.BodyHTML)
		useHTML = err == nil && strings.TrimSpace(extracted) == strings.TrimSpace(body)
	}
	if useHTML && e.BodyHTML != nil {
		if !utf8.ValidString(*e.BodyHTML) {
			return "", ErrInput
		}
		var err error
		body, err = cleanHTML(*e.BodyHTML)
		if err != nil {
			return "", err
		}
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	source := strings.Split(body, "\n")
	lines := []string{}
	for i, raw := range source {
		line := clean(raw)
		lower := strings.ToLower(line)
		if strings.TrimLeft(raw, "\t ") == "-- " || lower == "-----original message-----" {
			break
		}
		if strings.HasPrefix(lower, "from:") {
			headers := map[string]bool{}
			for _, next := range source[i:min(i+6, len(source))] {
				n := strings.ToLower(clean(next))
				for _, prefix := range []string{"from:", "sent:", "to:", "subject:"} {
					if strings.HasPrefix(n, prefix) {
						headers[prefix] = true
					}
				}
			}
			if len(headers) >= 3 {
				break
			}
		}
		if (lower == "sent from my iphone" || lower == "sent from my ipad" || lower == "sent from my android") && i >= len(source)-5 {
			break
		}
		if strings.HasPrefix(line, ">") || (strings.HasPrefix(lower, "on ") && strings.HasSuffix(lower, " wrote:")) {
			continue
		}
		// Do not delete useful inline answers or ordinary closings such as "Thanks".
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}
func PrepareRetrieval(e domain.Email) (RetrievalText, error) {
	result := RetrievalText{Passages: []Passage{}}
	if e.BodyState != "complete" {
		return result, ErrUnavailable
	}
	if len(e.Subject) > mail.MaxBodyBytes || !utf8.ValidString(e.Subject) {
		return result, ErrInput
	}
	body, err := RetrievalBody(e)
	if err != nil {
		return result, err
	}
	subject := clean(e.Subject)
	for {
		lower := strings.ToLower(subject)
		if strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "fw:") {
			subject = strings.TrimSpace(subject[3:])
		} else if strings.HasPrefix(lower, "fwd:") {
			subject = strings.TrimSpace(subject[4:])
		} else {
			break
		}
	}
	result.Hash = textHash("subject\x00" + subject + "\x00body\x00" + body)
	for _, part := range []struct{ kind, text string }{{"subject", subject}, {"body", body}} {
		text := part.text
		for start := 0; start < len(text); {
			end := min(start+PassageBytes, len(text))
			for end < len(text) && !utf8.RuneStart(text[end]) {
				end--
			}
			// Prefer a complete sentence/newline, then a word boundary near the end.
			if end < len(text) {
				cut := -1
				for i := end - 1; i >= start+200; i-- {
					if text[i] == '\n' || text[i] == '.' || text[i] == '!' || text[i] == '?' {
						cut = i + 1
						break
					}
				}
				if cut < 0 {
					for i := end - 1; i >= start+200; i-- {
						if text[i] == ' ' {
							cut = i
							break
						}
					}
				}
				if cut > start {
					end = cut
				}
			}
			chunk := strings.TrimSpace(text[start:end])
			if chunk != "" {
				result.Passages = append(result.Passages, Passage{Kind: part.kind, Text: chunk, Hash: textHash(chunk), Ordinal: len(result.Passages)})
			}
			if len(result.Passages) > MaxPassages {
				return RetrievalText{}, ErrInput
			}
			if end == len(text) {
				break
			}
			// Small overlap protects phrases crossing a boundary without copying a
			// whole paragraph/thread into every representation.
			next := end - 32
			for next < end && !utf8.RuneStart(text[next]) {
				next++
			}
			for next < end && text[next] != ' ' && text[next] != '\n' {
				next++
			}
			if next == end {
				start = end
			} else {
				start = next + 1
			}
		}
	}
	return result, nil
}
