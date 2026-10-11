package domain

// PhishingFeatures are local observations, never a score or a safety verdict.
// Strings may contain sensitive, untrusted mail data and must not be logged.
type PhishingFeatures struct {
	AccountID         AccountID
	EmailID           EmailID
	ExtractorVersion  string
	BodyState         string
	HTMLAvailable     bool
	Incomplete        bool
	Issues            []PhishingExtractionIssue
	FromDomain        string
	FromDomains       []string
	FromDomainSource  string
	ReplyToDomain     string
	ReplyToDomains    []string
	ReturnPathDomain  string
	ReturnPathDomains []string
	NullReturnPath    bool
	// Summaries are reported claims, not verified authentication. Missing,
	// malformed and contradictory evidence are unknown or conflicting.
	SPFResult             string
	SPFResults            []string
	DKIMResult            string
	DKIMResults           []string
	DMARCResult           string
	DMARCResults          []string
	AuthenticationResults []PhishingAuthenticationResults
	URLCount              int
	UniqueURLCount        int
	URLDomains            []string
	URLs                  []PhishingURL
	RelativeURLCount      int
	NonWebLinks           []PhishingNonWebLink
	PunycodeDomains       []string
	ExecutableAttachments []PhishingExecutableAttachment
	HTMLFormCount         int
	LanguageIndicators    []PhishingLanguageIndicator
}

type PhishingExtractionIssue struct {
	Code   string
	Source string
	Index  int
}

// HeaderIndex preserves provenance among ordered, potentially forged headers.
// Provenance is always unverified_header in this extractor; no authserv-id is
// automatically trusted and no DNS/authentication operation is performed.
type PhishingAuthenticationResults struct {
	HeaderIndex int
	HeaderName  string
	Raw         string
	AuthservID  string
	Version     string
	Provenance  string
	Malformed   bool
	NoResults   bool
	Methods     []PhishingAuthenticationMethod
}
type PhishingAuthenticationMethod struct {
	Method     string
	Version    string
	Result     string
	Reason     string
	Properties []PhishingAuthenticationProperty
}
type PhishingAuthenticationProperty struct{ Type, Name, Value string }

// URLs omit paths, query values and credentials. Sources identify observed
// references, including duplicates in alternative bodies. Domain is ASCII IDNA
// or a canonical IP literal. A malformed URL has no usable destination domain.
type PhishingURL struct {
	Source               string
	Scheme               string
	ProtocolRelative     bool
	Domain               string
	RegistrableDomain    string
	Malformed            bool
	IPAddress            bool
	Punycode             bool
	VisibleDomain        string
	VisibleLinkMismatch  bool
	CrossSiteMismatch    bool
	RedirectDomains      []string
	SuspiciousStructures []string
}
type PhishingNonWebLink struct{ Source, Scheme string }
type PhishingExecutableAttachment struct {
	AttachmentIndex int
	Filename        string
	Extension       string
	DoubleExtension bool
}
type PhishingLanguageIndicator struct{ Kind, Phrase, Source string }
