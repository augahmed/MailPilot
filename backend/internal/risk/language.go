package risk

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
)

// English lexical observations, including phrases also used by legitimate
// messages. These are deliberately not verdicts or credential-request proof.
var languageSignals = []struct {
	kind    string
	phrases []string
}{
	{"urgency", []string{"urgent", "immediately", "act now", "action required", "within 24 hours", "final notice", "last chance", "expires today"}},
	{"account_security", []string{"account suspended", "account locked", "security alert", "unusual activity", "unauthorized access", "verify your account", "account verification", "account security"}},
	{"credentials", []string{"password", "login credentials", "sign in", "log in", "verification code", "one time password", "credit card", "bank account", "enter your credentials", "confirm your password"}},
}

func word(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
func (x *featureExtraction) language(value, source string) {
	var normalized strings.Builder
	space := true
	for _, r := range value {
		if unicode.IsSpace(r) || r == '-' {
			if !space {
				normalized.WriteByte(' ')
			}
			space = true
		} else {
			normalized.WriteRune(unicode.ToLower(r))
			space = false
		}
	}
	text := normalized.String()
	for _, signal := range languageSignals {
		for _, phrase := range signal.phrases {
			if x.ctx.Err() != nil {
				return
			}
			for offset := 0; offset < len(text); {
				loc := strings.Index(text[offset:], phrase)
				if loc < 0 {
					break
				}
				start := offset + loc
				end := start + len(phrase)
				before, after := false, false
				if start > 0 {
					r, _ := utf8.DecodeLastRuneInString(text[:start])
					before = word(r)
				}
				if end < len(text) {
					r, _ := utf8.DecodeRuneInString(text[end:])
					after = word(r)
				}
				if !before && !after {
					x.f.LanguageIndicators = append(x.f.LanguageIndicators, domain.PhishingLanguageIndicator{Kind: signal.kind, Phrase: phrase, Source: source})
					break
				}
				offset = end
			}
		}
	}
}
