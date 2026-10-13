// Package embedding provides optional, explicit embedding preparation and caching.
// It has no startup worker, network adapter, search or generation capability.
package embedding

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/mail"
)

const PreprocessingVersion = "email-text-v1"
const MaxTextBytes = 16384

type PreparedText struct {
	Text string
	Hash string
}

// Prepare uses only subject, sender and cached body. Sender fields are untrusted
// descriptive context, never authentication evidence. No URLs are resolved.
func Prepare(e domain.Email) (PreparedText, error) {
	if e.BodyState == "unavailable" {
		return PreparedText{}, ErrUnavailable
	}
	for _, value := range []string{e.Subject, e.Sender.Name, e.Sender.Address, e.BodyText} {
		if len(value) > mail.MaxBodyBytes || !utf8.ValidString(value) {
			return PreparedText{}, ErrInput
		}
	}
	body, err := CleanBody(e)
	if err != nil {
		return PreparedText{}, err
	}

	text := "Subject: " + clip(clean(e.Subject), 1024) + "\nSender: " + clip(clean(e.Sender.Name), 256) +
		" <" + clip(clean(e.Sender.Address), 320) + ">\nBody:\n" + body
	text = clip(text, MaxTextBytes)
	return PreparedText{Text: text, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(PreprocessingVersion+"\x00"+text)))}, nil
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// CleanBody supplies the same bounded, quote/signature-trimmed body to rules
// and embeddings. It does not render HTML or fetch external resources.
func CleanBody(e domain.Email) (string, error) {
	if len(e.BodyText) > mail.MaxBodyBytes || !utf8.ValidString(e.BodyText) {
		return "", ErrInput
	}
	body := e.BodyText
	if strings.TrimSpace(body) == "" && e.BodyHTML != nil {
		if !utf8.ValidString(*e.BodyHTML) {
			return "", ErrInput
		}
		var err error
		body, err = cleanHTML(*e.BodyHTML)
		if err != nil {
			return "", ErrInput
		}
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = clean(line)
		lower := strings.ToLower(line)
		if lower == "--" || lower == "-----original message-----" || lower == "---------- forwarded message ---------" ||
			lower == "begin forwarded message:" || lower == "sent from my iphone" || lower == "sent from my ipad" ||
			lower == "sent from my android" || (strings.HasPrefix(lower, "on ") && strings.HasSuffix(lower, " wrote:")) {
			break
		}
		if line == "" || strings.HasPrefix(line, ">") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}
