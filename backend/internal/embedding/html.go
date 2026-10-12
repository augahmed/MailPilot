package embedding

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"mailpilot.local/core/internal/mail"
)

// Remove conventional quoted/signature containers before local text extraction.
// This is a size/depth-bounded heuristic, not an HTML renderer or sanitizer.
func cleanHTML(value string) (string, error) {
	if len(value) > mail.MaxBodyBytes {
		return "", ErrInput
	}
	root, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", ErrInput
	}
	var prune func(*html.Node, int) error
	prune = func(n *html.Node, depth int) error {
		if depth > 100 {
			return ErrInput
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
		return "", ErrInput
	}
	text, err := mail.HTMLText(out.String())
	if err != nil {
		return "", ErrInput
	}
	return text, nil
}
