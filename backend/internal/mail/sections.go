package mail

import (
	"context"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"mailpilot.local/core/internal/domain"
)

const MaxSections = 4096

// Section offsets address a named, reproducible normalized text projection, not
// raw MIME/HTML bytes. Kind is a conservative structural heuristic, not authorship
// authentication. Text is never quote/signature trimmed.
type Section struct {
	Kind, Source      string
	Index, Start, End int
	Text              string
}
type Projection struct {
	Sources       map[string]string
	Sections      []Section
	ContentStatus string
}
type textHint struct {
	start, end int
	kind       string
}

func canonicalLines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\x00", "\ufffd")
}
func comparisonText(s string) string { return strings.Join(strings.Fields(s), " ") }

// Sections operates only on already decoded cached mail. It does not decode the
// cache again, fetch absent MIME parts, inspect attachments, or modify Email.
func Sections(ctx context.Context, e domain.Email) (Projection, error) {
	p := Projection{Sources: map[string]string{}, ContentStatus: "unavailable"}
	if len(e.Subject) > 32768 || len(e.BodyText) > MaxBodyBytes || !utf8.ValidString(e.Subject) || !utf8.ValidString(e.BodyText) || (e.BodyHTML != nil && (len(*e.BodyHTML) > MaxBodyBytes || !utf8.ValidString(*e.BodyHTML))) {
		return Projection{}, ErrContent
	}
	if e.BodyState != "" && e.BodyState != "complete" && e.BodyState != "unavailable" {
		return Projection{}, ErrContent
	}
	add := func(sections []Section) error {
		for _, s := range sections {
			s.Index = len(p.Sections)
			p.Sections = append(p.Sections, s)
			if len(p.Sections) > MaxSections {
				return ErrContent
			}
		}
		return nil
	}
	subject := canonicalLines(e.Subject)
	p.Sources["subject"] = subject
	if strings.TrimSpace(subject) != "" {
		if err := add([]Section{{Kind: "subject", Source: "subject", End: len(subject), Text: subject}}); err != nil {
			return Projection{}, err
		}
	}
	plain := canonicalLines(e.BodyText)
	if strings.Count(plain, "\n") > 32768 {
		return Projection{}, ErrContent
	}
	p.Sources["body_text"] = plain
	var rendered string
	var hints []textHint
	if e.BodyHTML != nil {
		var err error
		rendered, hints, err = sectionHTML(ctx, *e.BodyHTML)
		if err != nil {
			return Projection{}, err
		}
		p.Sources["body_html"] = rendered
	}
	var body []Section
	var err error
	if strings.TrimSpace(plain) == "" || (rendered != "" && comparisonText(plain) == comparisonText(rendered)) {
		body, err = splitSections(ctx, "body_html", rendered, hints)
		if err != nil {
			return Projection{}, err
		}
	} else {
		body, err = splitSections(ctx, "body_text", plain, nil)
		if err != nil {
			return Projection{}, err
		}
		// MIME alternatives represent the same content: never concatenate both whole
		// bodies. Keep only additional HTML paragraphs, including forwarded evidence.
		known := comparisonText(plain)
		htmlSections, err := splitSections(ctx, "body_html", rendered, hints)
		if err != nil {
			return Projection{}, err
		}
		for _, s := range htmlSections {
			for offset := 0; offset < len(s.Text); {
				if err := ctx.Err(); err != nil {
					return Projection{}, err
				}
				end := strings.Index(s.Text[offset:], "\n\n")
				if end < 0 {
					end = len(s.Text)
				} else {
					end += offset + 2
				}
				text := s.Text[offset:end]
				normalized := comparisonText(text)
				if normalized != "" && !strings.Contains(known, normalized) {
					extra := s
					extra.Start += offset
					extra.End = s.Start + end
					extra.Text = text
					body = append(body, extra)
					if len(body) > MaxSections {
						return Projection{}, ErrContent
					}
					known += " " + normalized
				}
				offset = end
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Projection{}, err
	}
	if err := add(body); err != nil {
		return Projection{}, err
	}
	if len(body) > 0 {
		p.ContentStatus = "partial"
		if e.BodyState == "complete" {
			p.ContentStatus = "complete"
		}
	}
	return p, nil
}

func forwardLine(s string) bool {
	s = strings.ToLower(strings.TrimSpace(strings.TrimLeft(s, "> ")))
	if s == "begin forwarded message:" {
		return true
	}
	trimmed := strings.Trim(s, "- _\t")
	return trimmed == "forwarded message" || trimmed == "original message"
}
func replyLine(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "on ") && strings.HasSuffix(s, "wrote:")
}

// Gmail sometimes wraps the attribution over several physical lines.
func wrappedReplyLine(lines []string, index int) bool {
	line := strings.ToLower(strings.TrimSpace(lines[index]))
	if !strings.HasPrefix(line, "on ") {
		return false
	}
	size := len(line)
	for i := index + 1; i < len(lines) && i <= index+3; i++ {
		next := strings.ToLower(strings.TrimSpace(lines[i]))
		size += len(next)
		if next == "" || size > 1024 {
			return false
		}
		if strings.HasSuffix(next, "wrote:") {
			return true
		}
	}
	return false
}
func signatureLine(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "--" || s == "sent from my iphone" || s == "sent from my ipad" || s == "sent from my android"
}
func splitSections(ctx context.Context, source, text string, hints []textHint) ([]Section, error) {
	sections := []Section{}
	forwarded, reply, signature := false, false, false
	offset, hint := 0, 0
	lines := strings.SplitAfter(text, "\n")
	for lineIndex, line := range lines {
		if lineIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if line == "" {
			continue
		}
		kind := "authored"
		trimmed := strings.TrimSpace(line)
		for hint < len(hints) && hints[hint].end <= offset {
			hint++
		}
		if hint < len(hints) && hints[hint].start < offset+len(line) && hints[hint].kind != "" {
			kind = hints[hint].kind
		}
		if forwardLine(line) {
			forwarded = true
			signature = false
		} else if replyLine(line) || wrappedReplyLine(lines, lineIndex) {
			reply = true
			signature = false
		} else if signatureLine(line) {
			signature = true
		}
		switch {
		case forwarded:
			kind = "forwarded"
		case strings.HasPrefix(trimmed, ">") || reply:
			kind = "quoted"
		case signature:
			kind = "signature"
		}
		// Blank lines attach to their preceding section rather than inventing content.
		if trimmed == "" && len(sections) > 0 {
			kind = sections[len(sections)-1].Kind
		}
		if len(sections) > 0 && sections[len(sections)-1].Kind == kind {
			last := &sections[len(sections)-1]
			last.End += len(line)
			last.Text = text[last.Start:last.End]
		} else {
			sections = append(sections, Section{Kind: kind, Source: source, Start: offset, End: offset + len(line), Text: line})
		}
		if len(sections) > MaxSections {
			return nil, ErrContent
		}
		offset += len(line)
	}
	result := sections[:0]
	for _, s := range sections {
		if strings.TrimSpace(s.Text) != "" {
			result = append(result, s)
		}
	}
	return result, nil
}

// sectionHTML extracts visible text and structural hints. It is not a renderer
// or sanitizer. Never resolves a URL. Bound tokens, nesting and generated text.
func sectionHTML(ctx context.Context, value string) (string, []textHint, error) {
	type frame struct {
		name, kind string
		hidden     bool
	}
	stack := []frame{{kind: "authored"}}
	z := html.NewTokenizer(strings.NewReader(value))
	z.SetMaxBuf(MaxBodyBytes)
	var out strings.Builder
	hints := []textHint{}
	nodes := 0
	lastKind := ""
	write := func(s, kind string) {
		if s == "" {
			return
		}
		if kind != lastKind && out.Len() > 0 && strings.TrimSpace(s) != "" {
			start := out.Len()
			out.WriteString("\n\n")
			hints = append(hints, textHint{start, out.Len(), kind})
		}
		start := out.Len()
		out.WriteString(s)
		hints = append(hints, textHint{start, out.Len(), kind})
		lastKind = kind
	}
	for {
		if nodes%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		typ := z.Next()
		if typ == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", nil, ErrContent
			}
			break
		}
		nodes++
		if nodes > 100000 || len(stack) > 128 || out.Len() > MaxBodyBytes {
			return "", nil, ErrContent
		}
		tok := z.Token()
		top := stack[len(stack)-1]
		switch typ {
		case html.StartTagToken, html.SelfClosingTagToken:
			next := frame{name: tok.Data, kind: top.kind, hidden: top.hidden}
			switch tok.Data {
			case "script", "style", "head", "template", "iframe", "object", "noscript":
				next.hidden = true
			case "blockquote":
				next.kind = "quoted"
			}
			for _, a := range tok.Attr {
				if a.Key == "class" {
					for _, c := range strings.Fields(a.Val) {
						switch strings.ToLower(c) {
						case "gmail_quote", "yahoo_quoted":
							next.kind = "quoted"
						case "gmail_signature", "moz-signature":
							next.kind = "signature"
						}
					}
				}
			}
			if !next.hidden {
				if blockTag(tok.Data) {
					write("\n\n", next.kind)
				} else if tok.Data == "br" || tok.Data == "hr" {
					write("\n", next.kind)
				}
			}
			if typ == html.StartTagToken && !voidTag(tok.Data) {
				stack = append(stack, next)
			}
		case html.EndTagToken:
			if !top.hidden && blockTag(tok.Data) {
				write("\n\n", top.kind)
			}
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].name == tok.Data {
					stack = stack[:i]
					break
				}
			}
		case html.TextToken:
			if !top.hidden {
				// Collapse HTML whitespace without merging inline words or losing blocks.
				var text strings.Builder
				space := false
				for _, r := range tok.Data {
					if strings.ContainsRune(" \t\r\n\f\u00a0", r) {
						if !space {
							text.WriteByte(' ')
						}
						space = true
					} else {
						text.WriteRune(r)
						space = false
					}
				}
				write(canonicalLines(text.String()), top.kind)
			}
		}
	}
	if out.Len() > MaxBodyBytes {
		return "", nil, ErrContent
	}
	return out.String(), hints, nil
}
func blockTag(s string) bool {
	switch s {
	case "p", "div", "li", "tr", "td", "section", "article", "h1", "h2", "h3", "pre", "blockquote":
		return true
	}
	return false
}
func voidTag(s string) bool {
	switch s {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}
