package risk

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

func ignoredTag(name string) bool {
	switch name {
	case "script", "style", "head", "template", "iframe", "object":
		return true
	}
	return false
}
func urlAttribute(name string) bool {
	switch name {
	case "href", "src", "action", "formaction", "poster", "background":
		return true
	}
	return false
}
func (x *featureExtraction) html(value string) {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	tokenizer.SetMaxBuf(64 * 1024)
	var text, anchorText strings.Builder
	anchorIndex := -1
	anchorOpen := false
	ignored := []string{}
	flushAnchor := func() {
		if anchorOpen {
			x.linkText(anchorIndex, anchorText.String())
			anchorText.Reset()
			anchorIndex = -1
			anchorOpen = false
		}
	}
	for {
		if x.ctx.Err() != nil {
			return
		}
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				x.issue("html_token_limit_or_error", "body_html", -1)
			}
			if anchorOpen {
				x.issue("unclosed_anchor", "body_html", -1)
				flushAnchor()
			}
			break
		}
		token := tokenizer.Token()
		if kind == html.EndTagToken {
			if len(ignored) > 0 {
				if token.Data == ignored[len(ignored)-1] {
					ignored = ignored[:len(ignored)-1]
				}
				continue
			}
			if token.Data == "a" {
				flushAnchor()
			}
			if token.Data == "p" || token.Data == "div" || token.Data == "form" {
				text.WriteByte(' ')
			}
			continue
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			if ignoredTag(token.Data) {
				if kind == html.StartTagToken {
					if len(ignored) >= 64 {
						x.issue("html_depth_limit", "body_html", -1)
						break
					}
					ignored = append(ignored, token.Data)
				}
				continue
			}
			if len(ignored) > 0 {
				continue
			}
			if token.Data == "a" {
				if anchorOpen {
					x.issue("nested_anchor", "body_html", -1)
					flushAnchor()
				}
				anchorOpen = true
			}
			if token.Data == "form" {
				x.f.HTMLFormCount++
			}
			for _, attr := range token.Attr {
				if !urlAttribute(attr.Key) {
					continue
				}
				tag := token.Data
				if len(tag) > 32 {
					tag = "element"
				}
				index := x.addURL(attr.Val, "html_"+tag+"_"+attr.Key)
				if token.Data == "a" && attr.Key == "href" {
					anchorIndex = index
				}
			}
			// Inline markup must not split a displayed domain or a language phrase.
			if token.Data == "br" || token.Data == "p" || token.Data == "div" || token.Data == "form" {
				text.WriteByte(' ')
			}
		} else if kind == html.TextToken && len(ignored) == 0 {
			if text.Len() < maxBodyBytes {
				remaining := maxBodyBytes - text.Len()
				text.WriteString(token.Data[:min(len(token.Data), remaining)])
			} else {
				x.issue("html_text_limit", "body_html", -1)
			}
			if anchorOpen {
				if anchorText.Len()+len(token.Data) <= maxURLBytes {
					anchorText.WriteString(token.Data)
				} else {
					x.issue("anchor_text_limit", "body_html", -1)
					anchorIndex = -1
				}
			} else {
				x.scanTextURLs(token.Data, "html_text")
			}
		}
	}
	x.language(text.String(), "body_html")
}
