package mail

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"mailpilot.local/core/internal/domain"
)

// TextProjectionVersion versions both cleaned source offsets and reader paging.
// Original cached MIME/text and security-analysis inputs are never modified.
const TextProjectionVersion = "readable-v3"

var imagePlaceholder = regexp.MustCompile(`(?i)\[image:[ \t]*([^\]\r\n]*)\]`)
var textURL = regexp.MustCompile(`(?i)<?https?://[^\s<>]+>?`)
var emailLinkAnnotation = regexp.MustCompile(`(?i)<(?:mailto:)?[^<>\s@]+@[^<>\s@]+(?:\?[^<>\r\n]*)?>`)
var alternativeEdgePunctuation = regexp.MustCompile(`^[\pP]+|[\pP]+$`)
var localFootnote = regexp.MustCompile(`(?i)<#(?:m_[^>\s]*|[^>\s]*footnote[^>\s]*)>`)

type readableLine struct {
	text, kind, key         string
	image, newline, removed bool
	fallback                string
	group                   int
}

func imageDescription(label string) string {
	label = strings.Join(strings.Fields(canonicalLines(label)), " ")
	lower := strings.ToLower(label)
	switch lower {
	case "", "image", "spacer", "pixel", "tracking pixel", "transparent", "1x1", "blank", "logo":
		return ""
	}
	// Keep the brand's name while removing a decorative asset suffix.
	if strings.HasSuffix(lower, " logo") {
		return strings.TrimSpace(label[:len(label)-5])
	}
	for _, prefix := range []string{"logo of ", "logo for "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(label[len(prefix):])
		}
	}
	return label
}
func quotePrefix(line string) (string, string) {
	pos := 0
	for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t' || line[pos] == '>') {
		pos++
	}
	prefix := line[:pos]
	if !strings.Contains(prefix, ">") {
		return "", strings.TrimSpace(line)
	}
	return prefix, strings.TrimSpace(line[pos:])
}
func trackingParameter(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "utm_") || name == "gclid" || name == "fbclid" || name == "msclkid" || name == "mc_cid" || name == "mc_eid" || name == "mkt_tok"
}

// Resolve no redirects. Only transport-looking URLs on tracking hosts lose opaque
// queries. Ordinary URLs keep meaningful query parameters, paths and fragments.
func readableURL(token string) (clean string, tracking bool) {
	raw := strings.TrimSuffix(strings.TrimPrefix(token, "<"), ">")
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return token, false
	}
	values, queryErr := url.ParseQuery(u.RawQuery)
	host := strings.ToLower(u.Hostname())
	tracker := false
	labels := strings.Split(host, ".")
	for _, label := range labels[:max(0, len(labels)-2)] {
		switch label {
		case "click", "clicks", "track", "tracking", "trk", "links":
			tracker = true
		}
	}
	hasTracking := false
	for key := range values {
		if trackingParameter(key) {
			hasTracking = true
		}
	}
	if tracker && (len(values.Get("qs")) > 40 || len(u.RawQuery) > 80 || hasTracking) {
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		return u.String(), true
	}
	if queryErr == nil && hasTracking {
		for key := range values {
			if trackingParameter(key) {
				values.Del(key)
			}
		}
		u.RawQuery = values.Encode()
		u.ForceQuery = false
		return u.String(), false
	}
	return raw, false
}

// cleanProjection retains paragraph/section structure and meaningful image alt
// text, remapping hints to the cleaned projection rather than raw MIME offsets.
func cleanProjection(ctx context.Context, text string, hints []textHint) (string, []textHint, error) {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 32769 {
		return "", nil, ErrContent
	}
	cleaned := make([]readableLine, 0, len(lines))
	offset, hint := 0, 0
	group, lastKind := 0, ""
	for i, line := range lines {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		if line == "" {
			continue
		}
		row := readableLine{text: strings.TrimSuffix(line, "\n"), kind: "authored", newline: strings.HasSuffix(line, "\n")}
		first := offset + len(row.text) - len(strings.TrimLeft(row.text, " \t"))
		for hint < len(hints) && hints[hint].end <= first {
			hint++
		}
		if hint < len(hints) && hints[hint].start < offset+len(line) {
			row.kind = hints[hint].kind
			row.image = hints[hint].image
		}
		if row.kind != lastKind {
			group++
			lastKind = row.kind
		}
		row.group = group
		offset += len(line)
		prefix, content := quotePrefix(row.text)
		imageOnly := imagePlaceholder.MatchString(content) && strings.TrimSpace(imagePlaceholder.ReplaceAllString(content, "")) == ""
		if imageOnly {
			row.text = imagePlaceholder.ReplaceAllStringFunc(row.text, func(match string) string {
				label := imageDescription(imagePlaceholder.FindStringSubmatch(match)[1])
				if label == "" {
					return ""
				}
				return " " + label + " "
			})
			row.image = imageOnly
			if imageOnly {
				_, label := quotePrefix(row.text)
				row.text = prefix + strings.Join(strings.Fields(label), " ")
				if strings.TrimSpace(label) == "" {
					row.removed = true
				}
			}
		}
		row.text = localFootnote.ReplaceAllString(row.text, "")
		prefix, content = quotePrefix(row.text)
		matches := textURL.FindAllStringIndex(row.text, -1)
		if len(matches) > 0 {
			var out strings.Builder
			cursor := 0
			for _, span := range matches {
				token := row.text[span[0]:span[1]]
				replacement, tracking := readableURL(token)
				if tracking && content == token {
					row.removed = true
					row.fallback = prefix + replacement
				}
				out.WriteString(row.text[cursor:span[0]])
				out.WriteString(replacement)
				cursor = span[1]
			}
			out.WriteString(row.text[cursor:])
			row.text = out.String()
		}
		if row.image && imageDescription(strings.TrimSpace(row.text)) == "" {
			row.removed = true
		}
		row.key = comparisonText(strings.TrimSpace(strings.TrimLeft(row.text, "> ")))
		cleaned = append(cleaned, row)
	}
	// Deduplicate image-origin descriptions locally, never repeated authored prose
	// or the same text in another quoted/forwarded section.
	for i := range cleaned {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		row := &cleaned[i]
		if !row.image || row.removed || row.key == "" {
			continue
		}
		for j := max(0, i-12); j < min(len(cleaned), i+13); j++ {
			other := cleaned[j]
			if j != i && !other.removed && row.group == other.group && row.key == other.key && (!other.image || j < i) {
				row.removed = true
				break
			}
		}
	}
	// A URL-only message still has a readable destination, not an invented empty body.
	meaningful := false
	for _, row := range cleaned {
		if !row.removed && row.key != "" {
			meaningful = true
			break
		}
	}
	if !meaningful {
		for i := range cleaned {
			if cleaned[i].fallback != "" {
				cleaned[i].text = cleaned[i].fallback
				cleaned[i].removed = false
				break
			}
		}
	}
	var out strings.Builder
	remapped := []textHint{}
	blank := false
	for i, row := range cleaned {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		if row.removed {
			continue
		}
		empty := strings.TrimSpace(row.text) == ""
		if empty && blank {
			continue
		}
		blank = empty
		start := out.Len()
		out.WriteString(row.text)
		if row.newline {
			out.WriteByte('\n')
		}
		remapped = append(remapped, textHint{start: start, end: out.Len(), kind: row.kind})
		if out.Len() > MaxBodyBytes {
			return "", nil, ErrContent
		}
	}
	return out.String(), remapped, nil
}

// ReadableBody uses the same loss-preserving cleaned sections as Chunker.Build.
// Its result is text only: no HTML, active links, image loads or network calls.
func ReadableBody(ctx context.Context, e domain.Email) (string, string, error) {
	e.Subject = "" // Reader metadata has a separate subject bound; body cleaning does not.
	p, err := Sections(ctx, e)
	if err != nil {
		return "", "plain_text", err
	}
	var out strings.Builder
	format := "plain_text"
	for _, section := range p.Sections {
		if section.Source == "subject" {
			continue
		}
		if section.Source == "body_html" {
			format = "html_text"
		}
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") && !strings.HasPrefix(section.Text, "\n") {
			out.WriteString("\n\n")
		}
		out.WriteString(section.Text)
		if out.Len() > MaxBodyBytes {
			return "", format, ErrContent
		}
	}
	return strings.Trim(out.String(), "\n"), format, nil
}

// alternativeKey compares MIME representations, not arbitrary authored passages.
// Plain alternatives often encode emphasis with stars and put generated URLs on
// separate lines. These decorations must not make the same HTML prose look new.
// This key never replaces displayed text or source offsets.
func alternativeKey(text string) string {
	text = emailLinkAnnotation.ReplaceAllString(text, "")
	text = textURL.ReplaceAllString(text, "")
	var key strings.Builder
	for _, line := range strings.Split(text, "\n") {
		_, line = quotePrefix(line)
		for _, field := range strings.Fields(line) {
			// Strip edge emphasis only. Preserve arithmetic and identifiers such
			// as 2*3 and booking_A_B; those differences are actual evidence.
			field = alternativeEdgePunctuation.ReplaceAllStringFunc(field, func(edge string) string {
				return strings.ReplaceAll(strings.ReplaceAll(edge, "*", ""), "_", "")
			})
			if strings.TrimFunc(field, unicode.IsPunct) == "" {
				continue
			}
			if key.Len() > 0 {
				key.WriteByte(' ')
			}
			key.WriteString(field)
		}
	}
	return key.String()
}

// supplementAlternative keeps the primary representation's ordering and authored
// repetitions. Only passages from the other MIME representation are compared;
// quotes, forwards and signatures are matched within their own kind.
func supplementAlternative(ctx context.Context, primary, alternative []Section) ([]Section, error) {
	known := map[string]string{}
	for _, s := range primary {
		known[s.Kind] += " " + alternativeKey(s.Text)
	}
	for kind, text := range known {
		known[kind] = " " + comparisonText(text) + " "
	}
	result := primary
	for _, s := range alternative {
		for offset := 0; offset < len(s.Text); {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end := strings.Index(s.Text[offset:], "\n\n")
			if end < 0 {
				end = len(s.Text)
			} else {
				end += offset + 2
			}
			text := s.Text[offset:end]
			key := alternativeKey(text)
			// URL-only passages still carry evidence. Compare their literal text
			// rather than treating an empty comparison key as a match everywhere.
			duplicate := key != "" && strings.Contains(known[s.Kind], " "+key+" ")
			if key == "" {
				for _, original := range primary {
					if original.Kind == s.Kind && comparisonText(text) != "" && strings.Contains(original.Text, strings.TrimSpace(text)) {
						duplicate = true
						break
					}
				}
			}
			if strings.TrimSpace(text) != "" && !duplicate {
				extra := s
				extra.Start += offset
				extra.End = s.Start + end
				extra.Text = text
				result = append(result, extra)
				if len(result) > MaxSections {
					return nil, ErrContent
				}
			}
			offset = end
		}
	}
	return result, nil
}
