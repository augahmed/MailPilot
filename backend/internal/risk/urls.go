package risk

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"mailpilot.local/core/internal/domain"
)

var textURLPattern = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>"'\x00-\x1f]+`)
var redirectKeys = map[string]bool{"url": true, "redirect": true, "redirect_url": true, "redirect_uri": true, "next": true, "target": true, "dest": true, "destination": true, "continue": true, "return": true, "returnurl": true, "return_to": true}

func trimTextURL(raw string) string {
	raw = strings.TrimRight(raw, ".,;:!?")
	for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
		excess := strings.Count(raw, pair[1]) - strings.Count(raw, pair[0])
		for excess > 0 && strings.HasSuffix(raw, pair[1]) {
			raw = strings.TrimSuffix(raw, pair[1])
			excess--
		}
	}
	return raw
}
func (x *featureExtraction) scanTextURLs(value, source string) {
	// Find incrementally so a hostile body cannot allocate an unbounded list.
	for offset := 0; offset < len(value); {
		if x.ctx.Err() != nil {
			return
		}
		loc := textURLPattern.FindStringIndex(value[offset:])
		if loc == nil {
			return
		}
		raw := trimTextURL(value[offset+loc[0] : offset+loc[1]])
		offset += loc[1]
		if strings.HasPrefix(strings.ToLower(raw), "www.") {
			raw = "https://" + raw
		}
		x.addURL(raw, source)
		if len(x.f.URLs)+len(x.f.NonWebLinks)+x.f.RelativeURLCount >= maxURLs {
			x.issue("url_count_limit", source, -1)
			return
		}
	}
}
func (x *featureExtraction) addURL(raw, source string) int {
	if len(x.f.URLs)+len(x.f.NonWebLinks)+x.f.RelativeURLCount >= maxURLs {
		x.issue("url_count_limit", source, -1)
		return -1
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1
	}
	if len(raw) > maxURLBytes {
		x.issue("url_length_limit", source, -1)
		return -1
	}
	protocolRelative := strings.HasPrefix(raw, "//")
	parsedRaw := raw
	if protocolRelative {
		parsedRaw = "https:" + raw
	}
	u, err := url.Parse(parsedRaw)
	// A malformed web reference still counts, but never yields a usable domain.
	webPrefix := strings.HasPrefix(strings.ToLower(parsedRaw), "http:") || strings.HasPrefix(strings.ToLower(parsedRaw), "https:")
	if err == nil && u.Scheme == "" {
		x.f.RelativeURLCount++
		return -1
	}
	if err == nil && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		x.f.NonWebLinks = append(x.f.NonWebLinks, domain.PhishingNonWebLink{Source: source, Scheme: strings.ToLower(u.Scheme)})
		return -1
	}
	if err != nil && !webPrefix {
		x.issue("malformed_url", source, -1)
		return -1
	}
	f := domain.PhishingURL{Source: source, ProtocolRelative: protocolRelative, RedirectDomains: []string{}, SuspiciousStructures: []string{}}
	if strings.Contains(raw, "\\") {
		f.SuspiciousStructures = append(f.SuspiciousStructures, "backslash")
	}
	lower := strings.ToLower(raw)
	for _, escape := range []string{"%2f", "%5c", "%40", "%3a"} {
		if strings.Contains(lower, escape) {
			f.SuspiciousStructures = append(f.SuspiciousStructures, "encoded_delimiters")
			break
		}
	}
	if strings.Contains(lower, "%25") {
		f.SuspiciousStructures = append(f.SuspiciousStructures, "double_encoding")
	}
	if err != nil || u == nil || u.Opaque != "" || u.Host == "" || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		f.Malformed = true
	} else {
		f.Scheme = strings.ToLower(u.Scheme)
		f.Domain, f.IPAddress, f.Punycode = normalizedDomain(u.Hostname())
		if port := u.Port(); port != "" {
			n, e := strconv.Atoi(port)
			if e != nil || n < 1 || n > 65535 {
				f.Domain = ""
				f.IPAddress = false
				f.Punycode = false
			}
		}
		f.Malformed = f.Domain == ""
		if !f.Malformed {
			f.RegistrableDomain = registrableDomain(f.Domain)
			x.domains[f.Domain] = true
			if f.Punycode {
				x.punycode[f.Domain] = true
			}
			if len(f.Domain) > 80 {
				f.SuspiciousStructures = append(f.SuspiciousStructures, "long_hostname")
			}
			if !f.IPAddress && len(strings.Split(f.Domain, ".")) >= 6 {
				f.SuspiciousStructures = append(f.SuspiciousStructures, "many_subdomains")
			}
		}
		if u.User != nil {
			f.SuspiciousStructures = append(f.SuspiciousStructures, "embedded_credentials")
		}
		port := u.Port()
		if port != "" && !(f.Scheme == "https" && port == "443" || f.Scheme == "http" && port == "80") {
			f.SuspiciousStructures = append(f.SuspiciousStructures, "non_default_port")
		}
		values, queryErr := url.ParseQuery(u.RawQuery)
		if queryErr != nil {
			x.issue("malformed_url_query", source, -1)
		}
		for key, targets := range values {
			if !redirectKeys[strings.ToLower(key)] {
				continue
			}
			for _, target := range targets {
				if strings.HasPrefix(target, "//") {
					target = "https:" + target
				}
				dest, err := url.Parse(target)
				if err != nil || !(strings.EqualFold(dest.Scheme, "http") || strings.EqualFold(dest.Scheme, "https")) {
					continue
				}
				host, _, _ := normalizedDomain(dest.Hostname())
				if host != "" {
					f.RedirectDomains = append(f.RedirectDomains, host)
				}
			}
		}
		f.RedirectDomains = uniqueSorted(f.RedirectDomains)
		for _, host := range f.RedirectDomains {
			if f.Domain != "" && registrableDomain(host) != f.RegistrableDomain {
				f.SuspiciousStructures = append(f.SuspiciousStructures, "external_redirect_target")
				break
			}
		}
	}
	if f.Malformed {
		x.issue("malformed_url", source, -1)
	}
	f.SuspiciousStructures = uniqueSorted(f.SuspiciousStructures)
	// The key stays in memory only. No path, query or embedded credential is returned.
	key := parsedRaw
	if !f.Malformed {
		normalized := *u
		normalized.Scheme = f.Scheme
		normalized.Host = f.Domain
		if strings.Contains(f.Domain, ":") {
			normalized.Host = "[" + f.Domain + "]"
		}
		if u.Port() != "" {
			normalized.Host += ":" + u.Port()
		}
		key = normalized.String()
	}
	x.uniqueURLs[key] = true
	x.f.URLs = append(x.f.URLs, f)
	return len(x.f.URLs) - 1
}
func visibleDomain(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > maxURLBytes || strings.IndexFunc(text, unicode.IsSpace) >= 0 {
		return ""
	}
	if strings.HasPrefix(text, "//") {
		text = "https:" + text
	}
	if !strings.Contains(text, "://") {
		text = "https://" + text
	}
	u, err := url.Parse(text)
	if err != nil || u.User != nil || !(strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) {
		return ""
	}
	host, ip, _ := normalizedDomain(u.Hostname())
	if !ip && !strings.Contains(host, ".") {
		return ""
	}
	return host
}
func (x *featureExtraction) linkText(index int, text string) {
	if index < 0 || index >= len(x.f.URLs) {
		return
	}
	f := &x.f.URLs[index]
	f.VisibleDomain = visibleDomain(text)
	f.VisibleLinkMismatch = f.Domain != "" && f.VisibleDomain != "" && f.Domain != f.VisibleDomain
	f.CrossSiteMismatch = f.VisibleLinkMismatch && registrableDomain(f.VisibleDomain) != f.RegistrableDomain
}
