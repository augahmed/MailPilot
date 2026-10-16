package mail

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"mailpilot.local/core/internal/domain"
)

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

// CleanBody supplies bounded, quote/signature-trimmed text for deterministic rules.
// It does not render HTML or fetch external resources.
func CleanBody(e domain.Email) (string, error) {
	if len(e.BodyText) > MaxBodyBytes || !utf8.ValidString(e.BodyText) {
		return "", ErrContent
	}
	body := e.BodyText
	if strings.TrimSpace(body) == "" && e.BodyHTML != nil {
		if !utf8.ValidString(*e.BodyHTML) {
			return "", ErrContent
		}
		var err error
		body, err = cleanHTML(*e.BodyHTML)
		if err != nil {
			return "", ErrContent
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

// Remove conventional quoted/signature containers before local text extraction.
// This is a size/depth-bounded heuristic, not an HTML renderer or sanitizer.
func cleanHTML(value string) (string, error) {
	if len(value) > MaxBodyBytes {
		return "", ErrContent
	}
	root, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", ErrContent
	}
	var prune func(*html.Node, int) error
	prune = func(n *html.Node, depth int) error {
		if depth > 100 {
			return ErrContent
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			remove := child.Type == html.ElementNode && child.Data == "blockquote"
			if child.Type == html.ElementNode {
				for _, attr := range child.Attr {
					if attr.Key != "class" {
						continue
					}
					for _, class := range strings.Fields(attr.Val) {
						switch strings.ToLower(class) {
						case "gmail_quote", "gmail_signature", "yahoo_quoted", "moz-signature":
							remove = true
						}
					}
				}
			}
			if remove {
				n.RemoveChild(child)
			} else if err := prune(child, depth+1); err != nil {
				return err
			}
			child = next
		}
		return nil
	}
	if err := prune(root, 0); err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := html.Render(&out, root); err != nil {
		return "", ErrContent
	}
	text, err := HTMLText(out.String())
	if err != nil {
		return "", ErrContent
	}
	return text, nil
}
