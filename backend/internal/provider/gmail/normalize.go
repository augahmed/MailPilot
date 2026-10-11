package gmail

import (
	"encoding/base64"
	"golang.org/x/net/html/charset"
	"mime"
	netmail "net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/provider"
)

// Gmail response types stay private to this adapter.
type message struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	HistoryID    string   `json:"historyId"`
	InternalDate string   `json:"internalDate"`
	LabelIDs     []string `json:"labelIds"`
	Payload      part     `json:"payload"`
}
type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type part struct {
	PartID   string   `json:"partId"`
	MIMEType string   `json:"mimeType"`
	Filename string   `json:"filename"`
	Headers  []header `json:"headers"`
	Body     struct {
		Data         string `json:"data"`
		AttachmentID string `json:"attachmentId"`
		Size         *int64 `json:"size"`
	} `json:"body"`
	Parts []part `json:"parts"`
}

func headerValue(headers []header, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
func addresses(value string) []domain.EmailAddress {
	if value == "" {
		return nil
	}
	parser := netmail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}}
	parsed, err := parser.ParseList(value)
	if err != nil {
		return []domain.EmailAddress{{Address: value}}
	}
	result := make([]domain.EmailAddress, 0, len(parsed))
	for _, a := range parsed {
		result = append(result, domain.EmailAddress{Name: a.Name, Address: a.Address})
	}
	return result
}
func contentRevision(m message) string {
	// Gmail changes a draft's message ID when replacing it; include history as an
	// extra guard for drafts. Received message content is immutable under its ID.
	if slices.Contains(m.LabelIDs, "DRAFT") {
		return m.ID + ":" + m.HistoryID
	}
	return m.ID
}
func mappedLabels(account domain.AccountID, ids []string, known map[string]domain.EmailLabel) []domain.EmailLabel {
	result := make([]domain.EmailLabel, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || id == "" {
			continue
		}
		seen[id] = true
		l, ok := known[id]
		if !ok {
			l = domain.EmailLabel{AccountID: account, ProviderID: domain.ProviderLabelID(id), Name: id, Kind: domain.LabelKindUser}
			if !strings.HasPrefix(id, "Label_") {
				l.Kind = domain.LabelKindSystem
			}
		}
		result = append(result, l)
	}
	slices.SortFunc(result, func(a, b domain.EmailLabel) int { return strings.Compare(string(a.ProviderID), string(b.ProviderID)) })
	return result
}
func normalize(account domain.AccountID, m message, labels map[string]domain.EmailLabel) (domain.Email, error) {
	ms, err := strconv.ParseInt(m.InternalDate, 10, 64)
	if err != nil || m.ID == "" || len(m.Payload.Headers) > 2000 {
		return domain.Email{}, provider.ErrMalformed
	}
	e := domain.Email{AccountID: account, ProviderID: domain.ProviderMessageID(m.ID), ProviderThreadID: domain.ProviderThreadID(m.ThreadID), ContentRevision: contentRevision(m), BodyState: "complete", ReceivedAt: time.UnixMilli(ms).UTC(), Labels: mappedLabels(account, m.LabelIDs, labels)}
	if e.ReceivedAt.Year() < 1 || e.ReceivedAt.Year() > 9999 {
		return domain.Email{}, provider.ErrMalformed
	}
	for _, h := range m.Payload.Headers {
		if len(h.Name)+len(h.Value) > 32768 {
			return domain.Email{}, provider.ErrMalformed
		}
		e.Headers = append(e.Headers, domain.EmailHeader{Name: h.Name, Value: h.Value})
	}
	e.Subject = mailtext.DecodeHeader(headerValue(m.Payload.Headers, "Subject"))
	from := addresses(headerValue(m.Payload.Headers, "From"))
	if len(from) > 0 {
		e.Sender = from[0]
	}
	e.Recipients = addresses(headerValue(m.Payload.Headers, "To"))
	e.CCRecipients = addresses(headerValue(m.Payload.Headers, "Cc"))
	count := 0
	total := 0
	var walk func(part, int, bool) (string, string, error)
	walk = func(p part, depth int, forceAttachment bool) (string, string, error) {
		count++
		if count > 200 || depth > 20 {
			return "", "", provider.ErrMalformed
		}
		disposition, params, _ := mime.ParseMediaType(headerValue(p.Headers, "Content-Disposition"))
		filename := p.Filename
		if filename == "" {
			filename = params["filename"]
		}
		mimeType := strings.ToLower(p.MIMEType)
		attachment := forceAttachment || filename != "" || disposition == "attachment" || (!strings.HasPrefix(mimeType, "multipart/") && mimeType != "text/plain" && mimeType != "text/html")
		if attachment {
			if p.Body.Size != nil && *p.Body.Size < 0 {
				return "", "", provider.ErrMalformed
			}
			e.Attachments = append(e.Attachments, domain.AttachmentMetadata{ProviderID: domain.ProviderAttachmentID(p.Body.AttachmentID), PartID: p.PartID, Filename: mailtext.DecodeHeader(filename), MIMEType: mimeType, SizeBytes: p.Body.Size, Inline: disposition == "inline" || headerValue(p.Headers, "Content-ID") != "", ContentID: headerValue(p.Headers, "Content-ID")})
			return "", "", nil
		}
		if strings.HasPrefix(mimeType, "multipart/") {
			var texts, htmls []string
			children := p.Parts
			if mimeType == "multipart/related" && len(children) > 1 { // Only the root is body; retain resource metadata too.
				_, ctparams, _ := mime.ParseMediaType(headerValue(p.Headers, "Content-Type"))
				root := 0
				for i, c := range children {
					if ctparams["start"] != "" && headerValue(c.Headers, "Content-ID") == ctparams["start"] {
						root = i
					}
				}
				for i, c := range children {
					t, h, err := walk(c, depth+1, i != root)
					if err != nil {
						return "", "", err
					}
					texts = append(texts, t)
					htmls = append(htmls, h)
				}
			} else {
				for _, c := range children {
					t, h, err := walk(c, depth+1, false)
					if err != nil {
						return "", "", err
					}
					texts = append(texts, t)
					htmls = append(htmls, h)
				}
			}
			if mimeType == "multipart/alternative" {
				for _, t := range texts {
					if strings.TrimSpace(t) != "" {
						return t, strings.Join(htmls, "\n"), nil
					}
				}
				return "", strings.Join(htmls, "\n"), nil
			}
			// Mixed siblings may have distinct bodies; derive HTML fallback per sibling.
			for i := range texts {
				if strings.TrimSpace(texts[i]) == "" && htmls[i] != "" {
					texts[i], err = mailtext.HTMLText(htmls[i])
					if err != nil {
						return "", "", provider.ErrMalformed
					}
				}
			}
			return strings.TrimSpace(strings.Join(texts, "\n")), strings.Join(htmls, "\n"), nil
		}
		if p.Body.AttachmentID != "" && p.Body.Data == "" {
			e.BodyState = "unavailable"
			return "", "", nil
		}
		if len(p.Body.Data) > 2*mailtext.MaxBodyBytes {
			return "", "", provider.ErrMalformed
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
		if err != nil {
			return "", "", provider.ErrMalformed
		}
		total += len(data)
		if total > mailtext.MaxBodyBytes {
			return "", "", provider.ErrMalformed
		}
		ct := headerValue(p.Headers, "Content-Type")
		if ct == "" {
			ct = mimeType
		}
		value, err := mailtext.DecodeText(data, ct)
		if err != nil {
			return "", "", provider.ErrMalformed
		}
		if mimeType == "text/html" {
			return "", value, nil
		}
		return value, "", nil
	}
	text, html, err := walk(m.Payload, 0, false)
	if err != nil {
		return domain.Email{}, err
	}
	if html != "" {
		e.BodyHTML = &html
	}
	if strings.TrimSpace(text) == "" && html != "" {
		text, err = mailtext.HTMLText(html)
		if err != nil {
			return domain.Email{}, provider.ErrMalformed
		}
	}
	if len(text) > mailtext.MaxBodyBytes || len(html) > mailtext.MaxBodyBytes {
		return domain.Email{}, provider.ErrMalformed
	}
	e.BodyText = text
	if err = e.Validate(); err != nil {
		return domain.Email{}, provider.ErrMalformed
	}
	return e, nil
}
