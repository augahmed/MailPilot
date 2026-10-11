package risk

import (
	"net/mail"
	"net/netip"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

func normalizedDomain(host string) (string, bool, bool) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if strings.HasPrefix(strings.ToLower(host), "ipv6:") {
			host = host[5:]
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.String(), true, false
	}
	if host == "" || len(host) > 1024 || strings.ContainsAny(host, "/\\@:#?%[]") || strings.ContainsFunc(host, unicode.IsSpace) {
		return "", false, false
	}
	ascii, err := idna.Lookup.ToASCII(host)
	ascii = strings.ToLower(ascii)
	if err != nil || len(ascii) > 253 || ascii == "" {
		return "", false, false
	}
	if ip, err := netip.ParseAddr(ascii); err == nil {
		return ip.String(), true, false
	}
	punycode := false
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false, false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false, false
			}
		}
		if strings.HasPrefix(label, "xn--") {
			punycode = true
		}
	}
	return ascii, false, punycode
}
func registrableDomain(host string) string {
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return registrable
}
func mailboxDomains(value string) ([]string, bool) {
	addresses, err := mail.ParseAddressList(value)
	if err != nil || len(addresses) == 0 {
		return nil, false
	}
	domains := []string{}
	for _, a := range addresses {
		at := strings.LastIndexByte(a.Address, '@')
		if at < 1 || at == len(a.Address)-1 {
			return domains, false
		}
		d, _, _ := normalizedDomain(a.Address[at+1:])
		if d == "" {
			return domains, false
		}
		domains = append(domains, d)
	}
	return domains, true
}
func uniqueSorted(values []string) []string {
	result := append([]string{}, values...)
	slices.Sort(result)
	return slices.Compact(result)
}
func sortedKeys(values map[string]bool) []string {
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
func oneDomain(values []string, valid bool) string {
	if valid && len(values) == 1 {
		return values[0]
	}
	return ""
}
