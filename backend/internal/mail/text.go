// Package mail handles bounded, local-only decoding of hostile email content.
package mail

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const MaxBodyBytes = 1024 * 1024

var ErrContent = errors.New("unsupported or oversized email content")

func DecodeText(data []byte, contentType string) (string, error) {
	if len(data) > MaxBodyBytes {
		return "", ErrContent
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		return "", ErrContent
	}
	label := strings.ToLower(params["charset"])
	if label == "" || label == "utf-8" || label == "us-ascii" {
		if !utf8.Valid(data) {
			return "", ErrContent
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
	}
	reader, err := charset.NewReaderLabel(label, bytes.NewReader(data))
	if err != nil {
		return "", ErrContent
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, MaxBodyBytes+1))
	if err != nil || len(decoded) > MaxBodyBytes {
		return "", ErrContent
	}
	return strings.ReplaceAll(string(decoded), "\r\n", "\n"), nil
}
func DecodeHeader(value string) string {
	decoder := mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

// HTMLText extracts text only. It never resolves URLs or produces renderable HTML.
func HTMLText(value string) (string, error) {
	if len(value) > MaxBodyBytes {
		return "", ErrContent
	}
	z := html.NewTokenizer(strings.NewReader(value))
	z.SetMaxBuf(MaxBodyBytes)
	var out strings.Builder
	hidden := []string{}
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", ErrContent
			}
			break
		}
		token := z.Token()
		if kind == html.StartTagToken {
			switch token.Data {
			case "script", "style", "head", "template", "iframe", "object":
				hidden = append(hidden, token.Data)
			}
		}
		if len(hidden) > 0 {
			if kind == html.EndTagToken && token.Data == hidden[len(hidden)-1] {
				hidden = hidden[:len(hidden)-1]
			}
			continue
		}
		if kind == html.TextToken {
			out.WriteString(token.Data)
		}
		if kind == html.StartTagToken || kind == html.EndTagToken || kind == html.SelfClosingTagToken {
			switch token.Data {
			case "br", "p", "div", "li", "tr", "td", "section", "article", "h1", "h2", "h3", "hr":
				out.WriteByte('\n')
			}
		}
		if out.Len() > MaxBodyBytes {
			return "", ErrContent
		}
	}
	lines := []string{}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}
