package risk

import (
	"context"
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
)

const ExtractorVersion = "phishing-features-1"
const (
	maxHeaders          = 2000
	maxHeaderBytes      = 8192
	maxTotalHeaderBytes = 128 * 1024
	maxBodyBytes        = 1024 * 1024
	maxSubjectBytes     = 16 * 1024
	maxAttachments      = 200
	maxURLs             = 500
	maxURLBytes         = 4096
	maxIssues           = 256
)

// PhishingFeatureExtractor is a concrete offline parser. Scoring and provider
// provenance belong to separate consumers; no network or storage is involved.
type PhishingFeatureExtractor struct{}

func NewPhishingFeatureExtractor() *PhishingFeatureExtractor { return &PhishingFeatureExtractor{} }

type featureExtraction struct {
	ctx        context.Context
	f          domain.PhishingFeatures
	seenIssues map[domain.PhishingExtractionIssue]bool
	uniqueURLs map[string]bool
	domains    map[string]bool
	punycode   map[string]bool
}

func (x *featureExtraction) issue(code, source string, index int) {
	issue := domain.PhishingExtractionIssue{Code: code, Source: source, Index: index}
	x.f.Incomplete = true
	if len(x.f.Issues) >= maxIssues {
		limit := domain.PhishingExtractionIssue{Code: "issue_count_limit", Source: "extraction", Index: -1}
		if !x.seenIssues[limit] {
			x.f.Issues = append(x.f.Issues, limit)
			x.seenIssues[limit] = true
		}
		return
	}
	if !x.seenIssues[issue] {
		x.f.Issues = append(x.f.Issues, issue)
		x.seenIssues[issue] = true
	}
}
func (x *featureExtraction) text(value, source string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		x.issue("input_limit", source, -1)
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
		x.issue("invalid_utf8", source, -1)
	}
	return value
}
func (*PhishingFeatureExtractor) Extract(ctx context.Context, email domain.Email) (domain.PhishingFeatures, error) {
	if err := ctx.Err(); err != nil {
		return domain.PhishingFeatures{}, err
	}
	x := &featureExtraction{ctx: ctx, seenIssues: map[domain.PhishingExtractionIssue]bool{}, uniqueURLs: map[string]bool{}, domains: map[string]bool{}, punycode: map[string]bool{}, f: domain.PhishingFeatures{
		AccountID: email.AccountID, EmailID: email.ID, ExtractorVersion: ExtractorVersion, BodyState: email.BodyState, HTMLAvailable: email.BodyHTML != nil,
		Issues: []domain.PhishingExtractionIssue{}, FromDomains: []string{}, ReplyToDomains: []string{}, ReturnPathDomains: []string{},
		SPFResult: "unknown", SPFResults: []string{}, DKIMResult: "unknown", DKIMResults: []string{}, DMARCResult: "unknown", DMARCResults: []string{},
		AuthenticationResults: []domain.PhishingAuthenticationResults{}, URLs: []domain.PhishingURL{}, URLDomains: []string{}, PunycodeDomains: []string{}, NonWebLinks: []domain.PhishingNonWebLink{}, ExecutableAttachments: []domain.PhishingExecutableAttachment{}, LanguageIndicators: []domain.PhishingLanguageIndicator{},
	}}
	if email.BodyState != "complete" {
		x.issue("body_not_complete", "body", -1)
	}
	x.headers(email)
	subject := x.text(email.Subject, "subject", maxSubjectBytes)
	body := x.text(email.BodyText, "body_text", maxBodyBytes)
	x.language(subject, "subject")
	x.language(body, "body_text")
	x.scanTextURLs(subject, "subject")
	x.scanTextURLs(body, "body_text")
	if email.BodyHTML != nil {
		x.html(x.text(*email.BodyHTML, "body_html", maxBodyBytes))
	}
	x.attachments(email.Attachments)
	if err := ctx.Err(); err != nil {
		return domain.PhishingFeatures{}, err
	}
	x.f.URLCount = len(x.f.URLs)
	x.f.UniqueURLCount = len(x.uniqueURLs)
	x.f.URLDomains = sortedKeys(x.domains)
	x.f.PunycodeDomains = sortedKeys(x.punycode)
	x.f.SPFResults = uniqueSorted(x.f.SPFResults)
	x.f.DKIMResults = uniqueSorted(x.f.DKIMResults)
	x.f.DMARCResults = uniqueSorted(x.f.DMARCResults)
	x.f.SPFResult = authSummary(x.f.SPFResults)
	x.f.DKIMResult = authSummary(x.f.DKIMResults)
	x.f.DMARCResult = authSummary(x.f.DMARCResults)
	return x.f, nil
}
