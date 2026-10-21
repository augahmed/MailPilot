package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/risk"
)

const readerPageBytes = 16 * 1024
const readerResponseBytes = 48 * 1024

type ReadMailRequest struct {
	AccountID string `json:"accountId"`
	EmailID   string `json:"emailId"`
	Offset    int    `json:"offset"`
	Revision  string `json:"revision"`
}

func (p ReadMailRequest) Valid() bool {
	if p.AccountID == "" || len(p.AccountID) > 100 || p.EmailID == "" || len(p.EmailID) > 100 || p.Offset < 0 || p.Offset > mailtext.MaxBodyBytes {
		return false
	}
	if p.Revision == "" {
		return p.Offset == 0
	}
	_, err := hex.DecodeString(p.Revision)
	return len(p.Revision) == 64 && err == nil
}

type ReaderAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}
type ReaderAttachment struct {
	Filename  string `json:"filename"`
	MIMEType  string `json:"mimeType"`
	SizeBytes *int64 `json:"sizeBytes"`
	Inline    bool   `json:"inline"`
}
type ReaderLabel struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type ReaderCategory struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}
type ReaderMessage struct {
	Subject           string             `json:"subject"`
	Sender            ReaderAddress      `json:"sender"`
	Recipients        []ReaderAddress    `json:"recipients"`
	CC                []ReaderAddress    `json:"cc"`
	ReceivedAt        int64              `json:"receivedAt"`
	CachedAt          int64              `json:"cachedAt"`
	Attachments       []ReaderAttachment `json:"attachments"`
	Labels            []ReaderLabel      `json:"labels"`
	Categories        []ReaderCategory   `json:"categories"`
	CategoryState     string             `json:"categoryState"`
	Phishing          *RiskDTO           `json:"phishing"`
	BodyState         string             `json:"bodyState"`
	BodyFormat        string             `json:"bodyFormat"`
	BodyError         bool               `json:"bodyError"`
	MetadataTruncated bool               `json:"metadataTruncated"`
}
type ReadMailResponse struct {
	AccountID  string         `json:"accountId"`
	EmailID    string         `json:"emailId"`
	Revision   string         `json:"revision"`
	Offset     int            `json:"offset"`
	NextOffset int            `json:"nextOffset"`
	TotalBytes int            `json:"totalBytes"`
	Body       string         `json:"body"`
	Message    *ReaderMessage `json:"message"`
	Code       string         `json:"code"`
}

// CachedReader has no provider, OAuth refresh, mutation or model dependency.
// A session authorizes local reads; reconnecting remains a separate auth action.
type CachedReader struct {
	session func() (domain.AccountID, context.Context, error)
	emails  repository.EmailRepository
	review  *ClassificationReview
	risk    *risk.Engine
}

func NewCachedReader(session func() (domain.AccountID, context.Context, error), emails repository.EmailRepository, review *ClassificationReview, engine *risk.Engine) *CachedReader {
	return &CachedReader{session, emails, review, engine}
}
func (a *App) ReadMail(ctx context.Context, p ReadMailRequest) ReadMailResponse {
	if a.Reader == nil {
		return ReadMailResponse{Code: "unauthorized"}
	}
	return a.Reader.Read(ctx, p)
}
func readerError(code string) ReadMailResponse { return ReadMailResponse{Code: code} }
func (r *CachedReader) Read(ctx context.Context, p ReadMailRequest) ReadMailResponse {
	if !p.Valid() {
		return readerError("validation")
	}
	account, session, err := r.session()
	if err != nil || session == nil || session.Err() != nil || string(account) != p.AccountID {
		return readerError("unauthorized")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(session, cancel)
	defer stop()
	stored, err := r.emails.Get(ctx, account, domain.EmailID(p.EmailID))
	if session.Err() != nil {
		return readerError("unauthorized")
	}
	if errors.Is(err, repository.ErrNotFound) {
		return readerError("not_found")
	}
	if err != nil {
		return readerError("storage_error")
	}
	e := stored.Email
	if e.AccountID != account || string(e.ID) != p.EmailID {
		return readerError("not_found")
	}
	revision := mailtext.ReaderRevision(stored.ContentHash)
	if p.Revision != "" && p.Revision != revision {
		return readerError("stale")
	}
	m := &ReaderMessage{Recipients: []ReaderAddress{}, CC: []ReaderAddress{}, Attachments: []ReaderAttachment{}, Labels: []ReaderLabel{}, Categories: []ReaderCategory{}, ReceivedAt: e.ReceivedAt.UnixMilli(), CachedAt: stored.UpdatedAt.UnixMilli(), BodyState: e.BodyState, BodyFormat: "plain_text", CategoryState: "not_reviewed"}
	clip := func(value string, n int) string {
		value = strings.ToValidUTF8(value, "�")
		if len(value) <= n {
			return value
		}
		m.MetadataTruncated = true
		end := n - 3
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		return value[:end] + "…"
	}
	address := func(a domain.EmailAddress) ReaderAddress {
		return ReaderAddress{clip(a.Name, 256), clip(a.Address, 512)}
	}
	m.Subject = clip(e.Subject, 2048)
	m.Sender = address(e.Sender)
	for _, a := range e.Recipients[:min(100, len(e.Recipients))] {
		m.Recipients = append(m.Recipients, address(a))
	}
	for _, a := range e.CCRecipients[:min(100, len(e.CCRecipients))] {
		m.CC = append(m.CC, address(a))
	}
	for _, a := range e.Attachments[:min(200, len(e.Attachments))] {
		m.Attachments = append(m.Attachments, ReaderAttachment{clip(a.Filename, 512), clip(a.MIMEType, 128), a.SizeBytes, a.Inline})
	}
	for _, l := range e.Labels[:min(100, len(e.Labels))] {
		m.Labels = append(m.Labels, ReaderLabel{clip(l.Name, 256), string(l.Kind)})
	}
	for _, c := range e.Categories[:min(100, len(e.Categories))] {
		m.Categories = append(m.Categories, ReaderCategory{clip(c.Name, 100), "assigned"})
	}
	if len(e.Recipients) > 100 || len(e.CCRecipients) > 100 || len(e.Attachments) > 200 || len(e.Labels) > 100 || len(e.Categories) > 100 {
		m.MetadataTruncated = true
	}
	r.category(ctx, account, e, m)
	engine := r.risk
	if engine == nil {
		engine = risk.DefaultEngine()
	}
	assessment, err := assessRisk(ctx, r.emails, engine, account, e.ID)
	if err == nil && assessment.InputHash == stored.ContentHash {
		m.Phishing = riskDTO(assessment.Score, engine.Level(assessment.Score), assessment.AnalyzerVersion, assessment.Findings)
	}
	// The reader and indexer share one cleaned projection; raw cached mail remains intact.
	body, format, bodyErr := mailtext.ReadableBody(ctx, e)
	m.BodyFormat = format
	if bodyErr != nil {
		if session.Err() != nil {
			return readerError("unauthorized")
		}
		if ctx.Err() != nil {
			return readerError("cancelled")
		}
		body = ""
		m.BodyError = true
	}

	if p.Offset > len(body) || (p.Offset < len(body) && !utf8.RuneStart(body[p.Offset])) {
		return readerError("validation")
	}
	next := min(p.Offset+readerPageBytes, len(body))
	for next < len(body) && !utf8.RuneStart(body[next]) {
		next--
	}
	result := ReadMailResponse{AccountID: p.AccountID, EmailID: p.EmailID, Revision: revision, Offset: p.Offset, NextOffset: next, TotalBytes: len(body), Body: body[p.Offset:next], Message: m}
	// JSON escaping can multiply text size. Shorten the body page first; never
	// silently discard body text. Excessive metadata is explicitly marked omitted.
	for {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return readerError("storage_error")
		}
		if len(data) <= readerResponseBytes {
			break
		}
		if result.NextOffset-p.Offset > 1024 {
			next = p.Offset + (result.NextOffset-p.Offset)/2
			for next < len(body) && !utf8.RuneStart(body[next]) {
				next--
			}
			result.NextOffset = next
			result.Body = body[p.Offset:next]
			continue
		}
		m.MetadataTruncated = true
		switch {
		case len(m.Attachments) > 0:
			m.Attachments = m.Attachments[:len(m.Attachments)-1]
		case len(m.Recipients) > 0:
			m.Recipients = m.Recipients[:len(m.Recipients)-1]
		case len(m.CC) > 0:
			m.CC = m.CC[:len(m.CC)-1]
		case len(m.Labels) > 0:
			m.Labels = m.Labels[:len(m.Labels)-1]
		case len(m.Categories) > 0:
			m.Categories = m.Categories[:len(m.Categories)-1]
		default:
			return readerError("too_large")
		}
	}
	if session.Err() != nil {
		return readerError("unauthorized")
	}
	if ctx.Err() != nil {
		return readerError("cancelled")
	}
	return result
}
func (r *CachedReader) category(ctx context.Context, account domain.AccountID, e domain.Email, m *ReaderMessage) {
	if r.review == nil {
		m.CategoryState = "unavailable"
		return
	}
	run, err := r.review.results.Get(ctx, account, e.ID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return
	case errors.Is(err, repository.ErrConflict):
		m.CategoryState = "stale"
		return
	case err != nil:
		m.CategoryState = "unavailable"
		return
	}
	categories, err := r.review.categories.List(ctx, account)
	if err != nil {
		m.CategoryState = "unavailable"
		return
	}
	if run.ClassifierName != r.review.engine.Name() || run.ClassifierVersion != r.review.engine.Version() || !classification.FreshInput(run, e, categories) {
		m.CategoryState = "stale"
		return
	}
	if run.Semantic != nil && run.UsedClassifier == "user" {
		m.Categories = []ReaderCategory{}
		for _, id := range run.Semantic.Assigned {
			for _, c := range categories {
				if c.ID == id {
					m.Categories = append(m.Categories, ReaderCategory{c.Name, "assigned"})
				}
			}
		}
		m.CategoryState = "unclassified"
		if len(m.Categories) > 0 {
			m.CategoryState = "classified"
		}
		return
	}
	m.CategoryState = "unclassified"
	if len(run.Results) == 0 {
		return
	}
	for _, c := range categories {
		if c.ID == run.Results[0].CategoryID && c.Enabled {
			m.Categories = append(m.Categories, ReaderCategory{c.Name, "suggested"})
			m.CategoryState = "classified"
			return
		}
	}
	m.CategoryState = "stale"
}
