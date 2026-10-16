package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

type draftWire struct {
	ID      string `json:"id"`
	Message struct {
		ID       string `json:"id,omitempty"`
		ThreadID string `json:"threadId,omitempty"`
		Raw      string `json:"raw,omitempty"`
	} `json:"message"`
}

func validReply(d provider.DraftInput) bool {
	return d.Validate() == nil && len(d.Recipients) == 1 && len(d.CCRecipients) == 0 && len(d.BCCRecipients) == 0 && d.BodyHTML == nil && strings.TrimSpace(d.BodyText) != "" && strings.HasPrefix(d.MessageID, "<mailpilot-") && strings.HasSuffix(d.MessageID, "@mailpilot.invalid>") && provider.ValidMessageID(d.InReplyTo) && d.References != "" && validID(string(d.ThreadID))
}
func canonicalBody(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// Always encode/fold subject text, including ASCII that resembles an encoded
// word. This preserves the exact reviewed subject without RFC header injection.
func draftSubject(subject string) string {
	var words []string
	for len(subject) > 0 {
		n := min(45, len(subject))
		for n < len(subject) && !utf8.RuneStart(subject[n]) {
			n--
		}
		words = append(words, "=?utf-8?B?"+base64.StdEncoding.EncodeToString([]byte(subject[:n]))+"?=")
		subject = subject[n:]
	}
	return strings.Join(words, "\r\n ")
}
func draftMIME(d provider.DraftInput, from string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + (&mail.Address{Address: from}).String() + "\r\n")
	b.WriteString("To: " + (&mail.Address{Name: d.Recipients[0].Name, Address: d.Recipients[0].Address}).String() + "\r\n")
	b.WriteString("Subject: " + draftSubject(d.Subject) + "\r\nMessage-ID: " + d.MessageID + "\r\nIn-Reply-To: " + d.InReplyTo + "\r\nReferences: " + d.References + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(canonicalBody(d.BodyText)))
	_ = w.Close()
	return b.Bytes()
}

// CreateDraft is retired. Keep the adapter signature temporarily for historical
// package compatibility, but never dispatch a new Gmail draft write.
func (p *Provider) CreateDraft(context.Context, provider.DraftInput) (provider.DraftReference, error) {
	return provider.DraftReference{}, provider.ErrUnsupported
}

// No match is never proof of non-creation: Gmail indexing may lag, or a user may
// have edited/sent/deleted the draft. The caller must retain uncertain outcomes.
func (p *Provider) FindDraft(ctx context.Context, d provider.DraftInput) (provider.DraftReference, error) {
	if !validReply(d) {
		return provider.DraftReference{}, provider.ErrInvalidArgument
	}
	var list struct {
		Drafts []draftWire `json:"drafts"`
		Next   string      `json:"nextPageToken"`
	}
	if e := p.get(ctx, "/drafts", url.Values{"q": {"rfc822msgid:" + d.MessageID}, "maxResults": {"2"}}, &list); e != nil {
		return provider.DraftReference{}, e
	}
	if list.Next != "" || len(list.Drafts) > 1 {
		return provider.DraftReference{}, provider.ErrConflict
	}
	if len(list.Drafts) == 0 {
		return provider.DraftReference{}, provider.ErrNotFound
	}
	id := list.Drafts[0].ID
	if !validID(id) {
		return provider.DraftReference{}, provider.ErrMalformed
	}
	var found draftWire
	if e := p.get(ctx, "/drafts/"+url.PathEscape(id), url.Values{"format": {"raw"}}, &found); e != nil {
		return provider.DraftReference{}, e
	}
	if found.ID != id || !validID(found.Message.ID) || found.Message.ThreadID != string(d.ThreadID) || len(found.Message.Raw) > 128*1024 {
		return provider.DraftReference{}, provider.ErrConflict
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(found.Message.Raw, "="))
	if e != nil {
		return provider.DraftReference{}, provider.ErrMalformed
	}
	m, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		return provider.DraftReference{}, provider.ErrMalformed
	}
	subject, e := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if e != nil {
		return provider.DraftReference{}, provider.ErrMalformed
	}
	to, e := m.Header.AddressList("To")
	if e != nil || len(to) != 1 || to[0].Address != d.Recipients[0].Address || subject != d.Subject || m.Header.Get("Message-ID") != d.MessageID || m.Header.Get("In-Reply-To") != d.InReplyTo || m.Header.Get("References") != d.References || m.Header.Get("Cc") != "" || m.Header.Get("Bcc") != "" {
		return provider.DraftReference{}, provider.ErrConflict
	}
	contentType, params, e := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if e != nil || contentType != "text/plain" || !strings.EqualFold(params["charset"], "utf-8") {
		return provider.DraftReference{}, provider.ErrConflict
	}
	var reader io.Reader = m.Body
	switch strings.ToLower(m.Header.Get("Content-Transfer-Encoding")) {
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "7bit", "8bit", "":
	default:
		return provider.DraftReference{}, provider.ErrConflict
	}
	body, e := io.ReadAll(io.LimitReader(reader, 16385))
	if e != nil || len(body) > 16384 || canonicalBody(string(body)) != canonicalBody(d.BodyText) {
		return provider.DraftReference{}, provider.ErrConflict
	}
	return provider.DraftReference{AccountID: p.account, ProviderDraftID: found.ID, ProviderMessageID: domain.ProviderMessageID(found.Message.ID)}, nil
}

var _ provider.DraftFinder = (*Provider)(nil)
