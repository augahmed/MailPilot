package risk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func extract(t *testing.T, email domain.Email) domain.PhishingFeatures {
	t.Helper()
	f, err := NewPhishingFeatureExtractor().Extract(context.Background(), email)
	if err != nil {
		t.Fatal("unexpected extraction error")
	}
	return f
}
func fixture(t *testing.T, name string) domain.Email {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var email domain.Email
	if err = json.Unmarshal(data, &email); err != nil {
		t.Fatal(err)
	}
	return email
}
func hasLanguage(f domain.PhishingFeatures, kind, phrase string) bool {
	for _, v := range f.LanguageIndicators {
		if v.Kind == kind && v.Phrase == phrase {
			return true
		}
	}
	return false
}
func hasIssue(f domain.PhishingFeatures, code string) bool {
	for _, v := range f.Issues {
		if v.Code == code {
			return true
		}
	}
	return false
}
func TestSyntheticLegitimateFixture(t *testing.T) {
	email := fixture(t, "legitimate")
	f := extract(t, email)
	if f.AccountID != email.AccountID || f.EmailID != email.ID || f.ExtractorVersion != ExtractorVersion || f.Incomplete {
		t.Fatal("identity/version/completeness lost")
	}
	if f.FromDomain != "shop.example.com" || f.ReplyToDomain != f.FromDomain || f.ReturnPathDomain != f.FromDomain {
		t.Fatal("expected matching envelope domains")
	}
	if f.SPFResult != "pass" || f.DKIMResult != "pass" || f.DMARCResult != "pass" || f.AuthenticationResults[0].Provenance != "unverified_header" {
		t.Fatal("authentication must remain an unverified claim")
	}
	if f.URLCount != 2 || f.UniqueURLCount != 1 || !reflect.DeepEqual(f.URLDomains, []string{"shop.example.com"}) {
		t.Fatal("alternative body URL accounting changed")
	}
	for _, u := range f.URLs {
		if u.VisibleLinkMismatch || u.IPAddress || u.Punycode || u.Malformed || len(u.SuspiciousStructures) > 0 {
			t.Fatal("unexpected URL signal for ordinary matching link")
		}
	}
	if f.HTMLFormCount != 0 || len(f.ExecutableAttachments) != 0 || len(f.LanguageIndicators) != 0 {
		t.Fatal("unexpected fixture signals")
	}
}
func TestSyntheticSuspiciousFixture(t *testing.T) {
	f := extract(t, fixture(t, "suspicious"))
	if f.Incomplete || f.FromDomain == f.ReplyToDomain || f.FromDomain == f.ReturnPathDomain || f.SPFResult != "fail" || f.DKIMResult != "fail" || f.DMARCResult != "fail" {
		t.Fatal("expected distinct domains and authentication claims")
	}
	if f.URLCount != 5 || f.UniqueURLCount != 5 || f.HTMLFormCount != 1 || len(f.ExecutableAttachments) != 1 || !f.ExecutableAttachments[0].DoubleExtension {
		t.Fatal("missing structural signals")
	}
	if !hasLanguage(f, "urgency", "act now") || !hasLanguage(f, "account_security", "verify your account") || !hasLanguage(f, "credentials", "password") {
		t.Fatal("missing language indicators")
	}
	var ips, puny, mismatch int
	for _, u := range f.URLs {
		if u.IPAddress {
			ips++
		}
		if u.Punycode {
			puny++
		}
		if u.VisibleLinkMismatch && u.CrossSiteMismatch {
			mismatch++
			if !slices.Contains(u.SuspiciousStructures, "external_redirect_target") || !slices.Contains(u.SuspiciousStructures, "non_default_port") {
				t.Fatal("missing URL structures")
			}
		}
	}
	if ips != 3 || puny != 1 || mismatch != 1 {
		t.Fatal("missing IP/IDNA/link mismatch observations")
	}
}
func TestMalformedFixtureRetainsPartialEvidence(t *testing.T) {
	f := extract(t, fixture(t, "malformed"))
	if !f.Incomplete || f.FromDomain != "" || f.ReplyToDomain != "" || f.SPFResult != "unknown" || f.DMARCResult != "unknown" {
		t.Fatal("malformed evidence must not produce domain/authentication summaries")
	}
	if !hasIssue(f, "malformed_header") || !hasIssue(f, "malformed_authentication") || !hasIssue(f, "malformed_url") || !hasIssue(f, "unclosed_anchor") {
		t.Fatal("missing parse limitations")
	}
	if !slices.Contains(f.URLDomains, "valid.example") || !slices.Contains(f.URLDomains, "destination.example") {
		t.Fatal("valid partial observations were dropped")
	}
}
func TestAddressHeaders(t *testing.T) {
	cases := []struct {
		name                 string
		headers              []domain.EmailHeader
		sender, want, source string
		domains              int
		null                 bool
	}{
		{name: "sender fallback", sender: "User@EXAMPLE.COM", want: "example.com", source: "normalized_sender", domains: 1},
		{name: "decoded and folded", headers: []domain.EmailHeader{{Name: "From", Value: "=?UTF-8?Q?Example_User?=\r\n <user@Example.COM>"}}, want: "example.com", source: "from_header", domains: 1},
		{name: "forged duplicate", headers: []domain.EmailHeader{{Name: "From", Value: "a@example.com"}, {Name: "FROM", Value: "b@example.org"}}, source: "from_header", domains: 2},
		{name: "same domain list", headers: []domain.EmailHeader{{Name: "From", Value: "a@example.com, b@example.com"}}, want: "example.com", source: "from_header", domains: 1},
		{name: "malformed suppresses fallback", headers: []domain.EmailHeader{{Name: "From", Value: "not-an-address"}}, sender: "a@example.com", source: "from_header"},
		{name: "null return path", headers: []domain.EmailHeader{{Name: "Return-Path", Value: "<>"}}, null: true},
		{name: "IDNA", sender: "user@bücher.example", want: "xn--bcher-kva.example", source: "normalized_sender", domains: 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := extract(t, domain.Email{BodyState: "complete", Headers: tt.headers, Sender: domain.EmailAddress{Address: tt.sender}})
			if f.FromDomain != tt.want || f.FromDomainSource != tt.source || len(f.FromDomains) != tt.domains || f.NullReturnPath != tt.null {
				t.Fatal("unexpected address observation")
			}
		})
	}
}
func TestAuthenticationGrammarAndTrust(t *testing.T) {
	cases := []struct {
		name, value, want string
		malformed         bool
	}{
		{"ordinary", "mx.google.com; spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com; dmarc=pass header.from=example.com", "pass", false},
		{"comments cannot inject", "mail.example.test (spf=fail (nested)); spf=pass reason=\"not a failure; dmarc=fail\" smtp.mailfrom=example.com", "pass", false},
		{"folding", "mail.example.test;\r\n\tspf / 1 = softfail smtp.mailfrom = example.com", "softfail", false},
		{"missing results", "mail.example.test; none", "unknown", false},
		{"broken quotes", "mail.example.test; spf=pass reason=\"unfinished; spf=fail", "unknown", true},
		{"broken comments", "mail.example.test (unfinished; spf=fail", "unknown", true},
		{"split method identifier", "mail.example.test; s p f=fail", "unknown", true},
		{"split property identifier", "mail.example.test; spf=fail sm tp.mailfrom=example.com", "unknown", true},
		{"unknown result", "mail.example.test; spf=excellent", "unknown", false},
		{"future header version", "mail.example.test 2; spf=pass", "unknown", false},
		{"future method version", "mail.example.test; spf/2=pass", "unknown", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := extract(t, domain.Email{BodyState: "complete", Headers: []domain.EmailHeader{{Name: "Authentication-Results", Value: tt.value}}})
			if f.SPFResult != tt.want || len(f.AuthenticationResults) != 1 || f.AuthenticationResults[0].Malformed != tt.malformed || f.AuthenticationResults[0].Provenance != "unverified_header" {
				t.Fatal("unexpected authentication claim")
			}
		})
	}
	f := extract(t, domain.Email{BodyState: "complete", Headers: []domain.EmailHeader{{Name: "Authentication-Results", Value: "a.example; spf=pass; dkim=fail"}, {Name: "Authentication-Results", Value: "b.example; spf=fail; dkim=pass"}, {Name: "Received-SPF", Value: "pass (mail.example.test: synthetic claim) client-ip=192.0.2.1; envelope-from=sender@example.com; helo=mail.example.test"}}})
	if f.SPFResult != "conflicting" || f.DKIMResult != "conflicting" || f.DMARCResult != "unknown" || len(f.AuthenticationResults) != 3 || len(f.AuthenticationResults[2].Methods[0].Properties) != 3 {
		t.Fatal("duplicate/Received-SPF evidence lost")
	}
	empty := extract(t, domain.Email{BodyState: "complete"})
	if empty.SPFResult != "unknown" || empty.DKIMResult != "unknown" || empty.DMARCResult != "unknown" || empty.Incomplete {
		t.Fatal("missing authentication is not a failure")
	}
}
func TestURLSignals(t *testing.T) {
	cases := []struct {
		name, target, visible, domain        string
		ip, puny, mismatch, cross, malformed bool
		flags                                []string
	}{
		{name: "case and trailing dot", target: "https://SHOP.example.com./p", visible: "https://shop.example.com/p", domain: "shop.example.com"},
		{name: "same site difference", target: "https://login.example.co.uk/p", visible: "https://www.example.co.uk", domain: "login.example.co.uk", mismatch: true},
		{name: "cross site", target: "https://evil.example/", visible: "https://example.com/", domain: "evil.example", mismatch: true, cross: true},
		{name: "ordinary descriptive label", target: "https://example.com/", visible: "View your receipt", domain: "example.com"},
		{name: "IPv4", target: "http://192.0.2.10/", visible: "192.0.2.10", domain: "192.0.2.10", ip: true},
		{name: "IPv6", target: "https://[2001:db8::1]/", visible: "https://[2001:db8::1]", domain: "2001:db8::1", ip: true},
		{name: "Unicode equivalent", target: "https://bücher.example/", visible: "https://xn--bcher-kva.example/", domain: "xn--bcher-kva.example", puny: true},
		{name: "protocol relative", target: "//example.com/p", visible: "example.com", domain: "example.com"},
		{name: "userinfo", target: "https://bank.example.com@evil.example:8443/p", visible: "bank.example.com", domain: "evil.example", mismatch: true, cross: true, flags: []string{"embedded_credentials", "non_default_port"}},
		{name: "nested escapes", target: "https://example.com/p%252f%40", domain: "example.com", flags: []string{"encoded_delimiters", "double_encoding"}},
		{name: "redirect", target: "https://example.com/?next=https%3A%2F%2Fevil.example%2Fsecret", domain: "example.com", flags: []string{"encoded_delimiters", "external_redirect_target"}},
		{name: "same site redirect", target: "https://example.com/?next=https://login.example.com/", domain: "example.com"},
		{name: "many subdomains", target: "https://a.b.c.d.example.com/", domain: "a.b.c.d.example.com", flags: []string{"many_subdomains"}},
		{name: "invalid host", target: "https://[broken", malformed: true},
		{name: "invalid percent escape", target: "https://%zz.example/", malformed: true},
		{name: "port out of range", target: "https://example.com:999999/", malformed: true, flags: []string{"non_default_port"}},
		{name: "backslash", target: "https://example.com\\@evil.example/", malformed: true, flags: []string{"backslash"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := "<a href=\"" + tt.target + "\">" + tt.visible + "</a>"
			f := extract(t, domain.Email{BodyState: "complete", BodyHTML: &body})
			if len(f.URLs) != 1 {
				t.Fatal("missing URL observation")
			}
			u := f.URLs[0]
			if u.Domain != tt.domain || u.IPAddress != tt.ip || u.Punycode != tt.puny || u.VisibleLinkMismatch != tt.mismatch || u.CrossSiteMismatch != tt.cross || u.Malformed != tt.malformed {
				t.Fatal("unexpected URL observation")
			}
			for _, flag := range tt.flags {
				if !slices.Contains(u.SuspiciousStructures, flag) {
					t.Fatal("missing structural flag")
				}
			}
			if tt.name == "same site redirect" && slices.Contains(u.SuspiciousStructures, "external_redirect_target") {
				t.Fatal("same site redirect incorrectly flagged")
			}
		})
	}
}
func TestHTMLAndTextBoundaries(t *testing.T) {
	html := `<head><title>urgent password</title></head><script>https://hidden.example/</script><style>urgent</style><template><form></form></template><p>Verify your <b>account</b>.</p><a href="https://destination.example/">https://<span>visible</span>.example</a><a href="/local">local</a><a href="mailto:user@example.com">email</a><a href="javascript:alert(1)">script</a><img src="//image.example/p"><form action="/submit"><button formaction="https://submit.example/">Submit</button></form>`
	f := extract(t, domain.Email{BodyState: "complete", Subject: "Documentation (https://subject.example/p).", BodyText: "See https://plain.example/p?x=1&y=2, and www.example.com/path.", BodyHTML: &html})
	if f.URLCount != 6 || f.RelativeURLCount != 2 || len(f.NonWebLinks) != 2 || f.HTMLFormCount != 1 || !hasLanguage(f, "account_security", "verify your account") || hasLanguage(f, "urgency", "urgent") || hasLanguage(f, "credentials", "password") {
		t.Fatal("HTML/text boundary handling changed")
	}
	if slices.Contains(f.URLDomains, "hidden.example") || slices.Contains(f.URLDomains, "visible.example") {
		t.Fatal("non-destination text counted as destination")
	}
	if !f.URLs[3].VisibleLinkMismatch {
		t.Fatal("inline markup broke displayed-domain comparison")
	}
}
func TestBenignLanguageAndAttachments(t *testing.T) {
	f := extract(t, domain.Email{BodyState: "complete", Subject: "Urgently urgentlyness passwords", BodyText: "A legitimate security alert: sign in with your password. No urgency implied.", Attachments: []domain.AttachmentMetadata{{Filename: "photo.jpg"}, {Filename: "receipt.pdf"}, {Filename: "archive.zip"}, {Filename: "instructions.exe.txt"}}})
	if hasLanguage(f, "urgency", "urgent") || len(f.ExecutableAttachments) != 0 {
		t.Fatal("substring or safe-suffix false positive")
	}
	if !hasLanguage(f, "credentials", "password") || !hasLanguage(f, "account_security", "security alert") {
		t.Fatal("benign lexical evidence must still be reported without a verdict")
	}
	cases := []string{"INVOICE.pdf.EXE", "folder\\notice.CMD ", "folder/run.ps1", "installer.pkg", "script.js", "application.app"}
	attachments := []domain.AttachmentMetadata{}
	for _, name := range cases {
		attachments = append(attachments, domain.AttachmentMetadata{Filename: name})
	}
	f = extract(t, domain.Email{BodyState: "complete", Attachments: attachments})
	if len(f.ExecutableAttachments) != len(cases) || f.ExecutableAttachments[0].Extension != ".exe" || !f.ExecutableAttachments[0].DoubleExtension {
		t.Fatal("missing executable extensions")
	}
}
func TestLimitsCompletenessAndCancellation(t *testing.T) {
	email := domain.Email{BodyState: "unavailable", BodyText: strings.Repeat("https://example.com/ ", maxURLs+1), Headers: make([]domain.EmailHeader, maxHeaders+1), Attachments: make([]domain.AttachmentMetadata, maxAttachments+1)}
	email.Headers[0] = domain.EmailHeader{Name: "From", Value: "user@example.com"}
	f := extract(t, email)
	if !f.Incomplete || f.FromDomain != "" || f.URLCount != maxURLs || !hasIssue(f, "body_not_complete") || !hasIssue(f, "header_count_limit") || !hasIssue(f, "url_count_limit") || !hasIssue(f, "attachment_count_limit") {
		t.Fatal("limits/completeness must be explicit")
	}
	longHTML := "<a href=\"" + strings.Repeat("x", 70*1024) + "\">x</a>"
	f = extract(t, domain.Email{BodyState: "complete", BodyHTML: &longHTML})
	if !hasIssue(f, "html_token_limit_or_error") {
		t.Fatal("HTML token limit not recorded")
	}
	invalid := string([]byte{'x', 0xff, 0xfe})
	f = extract(t, domain.Email{BodyState: "complete", BodyText: invalid})
	if !hasIssue(f, "invalid_utf8") {
		t.Fatal("invalid UTF-8 not reported")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewPhishingFeatureExtractor().Extract(ctx, fixture(t, "legitimate"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not honored")
	}
}
func TestDeterministicIndependentExtraction(t *testing.T) {
	engine := NewPhishingFeatureExtractor()
	email := fixture(t, "suspicious")
	first, err := engine.Extract(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	second := extract(t, email)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("nondeterministic extraction")
	}
	first.URLs[0].Domain = "changed"
	third := extract(t, email)
	if !reflect.DeepEqual(second, third) {
		t.Fatal("results share mutable state")
	}
}
func FuzzExtractionNeverPanics(f *testing.F) {
	f.Add("mail.example.test; spf=pass", `<a href="https://example.com">https://other.example</a>`, "User <user@example.com>")
	f.Add("(\x00", `<form><a href='http://[broken'>`, `broken <`)
	f.Fuzz(func(t *testing.T, auth, body, address string) {
		if len(auth) > maxHeaderBytes*2 || len(body) > maxBodyBytes*2 || len(address) > maxHeaderBytes*2 {
			t.Skip()
		}
		email := domain.Email{BodyState: "complete", BodyText: body, BodyHTML: &body, Headers: []domain.EmailHeader{{Name: "Authentication-Results", Value: auth}, {Name: "From", Value: address}, {Name: "Reply-To", Value: address}, {Name: "Return-Path", Value: address}}, Attachments: []domain.AttachmentMetadata{{Filename: address}}}
		result := extract(t, email)
		if result.URLCount > maxURLs {
			t.Fatal("URL limit exceeded")
		}
	})
}

func TestAdditionalMalformedAndBoundedInputs(t *testing.T) {
	cases := []struct {
		name  string
		email domain.Email
		issue string
	}{
		{"oversized body", domain.Email{BodyText: strings.Repeat("x", maxBodyBytes+1)}, "input_limit"},
		{"oversized subject", domain.Email{Subject: strings.Repeat("x", maxSubjectBytes+1)}, "input_limit"},
		{"oversized header", domain.Email{Headers: []domain.EmailHeader{{Name: "From", Value: strings.Repeat("x", maxHeaderBytes+1)}}}, "header_value_limit"},
		{"total headers", domain.Email{Headers: []domain.EmailHeader{{Name: "Ignored", Value: strings.Repeat("x", maxTotalHeaderBytes+1)}}}, "header_bytes_limit"},
		{"oversized URL", domain.Email{BodyText: "https://example.com/" + strings.Repeat("a", maxURLBytes+1)}, "url_length_limit"},
		{"oversized filename", domain.Email{Attachments: []domain.AttachmentMetadata{{Filename: strings.Repeat("x", 1020) + ".exe.txt"}}}, "input_limit"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tt.email.BodyState = "complete"
			f := extract(t, tt.email)
			if !hasIssue(f, tt.issue) {
				t.Fatal("missing bounded input observation")
			}
			if len(f.ExecutableAttachments) > 0 {
				t.Fatal("truncation created false executable suffix")
			}
		})
	}
	html := `<a HREF="https://first.example/" href="https://second.example/">https://first.example/</a>`
	f := extract(t, domain.Email{BodyState: "complete", BodyHTML: &html})
	if !reflect.DeepEqual(f.URLDomains, []string{"first.example"}) {
		t.Fatal("tokenizer first-attribute handling must be deterministic")
	}
	headers := make([]domain.EmailHeader, maxIssues+20)
	for i := range headers {
		headers[i] = domain.EmailHeader{Name: "From", Value: "malformed"}
	}
	f = extract(t, domain.Email{BodyState: "complete", Headers: headers})
	if len(f.Issues) != maxIssues+1 || !hasIssue(f, "issue_count_limit") {
		t.Fatal("issue collection must be bounded")
	}
	longHost := strings.Repeat("a", 40) + "." + strings.Repeat("b", 40) + ".example"
	f = extract(t, domain.Email{BodyState: "complete", BodyText: "https://" + longHost + "/"})
	if !slices.Contains(f.URLs[0].SuspiciousStructures, "long_hostname") {
		t.Fatal("long hostname not reported")
	}
	// Full-width IDNA digits normalize before IP classification.
	f = extract(t, domain.Email{BodyState: "complete", BodyText: "https://１９２.０.２.１０/"})
	if !f.URLs[0].IPAddress || f.URLs[0].Domain != "192.0.2.10" {
		t.Fatal("IDNA-normalized IP not recognized")
	}
}
