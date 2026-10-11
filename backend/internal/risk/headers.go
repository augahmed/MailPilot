package risk

import (
	"strings"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
)

// Unfold genuine continuation lines; a new header line is never treated as
// part of an address or an authentication assertion.
func unfoldedHeader(value string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\r' || value[i] == '\n' {
			if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
				i++
			}
			if i+1 >= len(value) || (value[i+1] != ' ' && value[i+1] != '\t') {
				return "", false
			}
			out.WriteByte(' ')
			for i+1 < len(value) && (value[i+1] == ' ' || value[i+1] == '\t') {
				i++
			}
			continue
		}
		if value[i] == 0 {
			return "", false
		}
		out.WriteByte(value[i])
	}
	return out.String(), utf8.ValidString(value)
}
func (x *featureExtraction) headers(email domain.Email) {
	fromPresent := false
	fromOK, replyOK, returnOK := true, true, true
	total := 0
	if len(email.Headers) > maxHeaders {
		x.issue("header_count_limit", "headers", maxHeaders)
		fromOK, replyOK, returnOK = false, false, false
	}
	for i, h := range email.Headers[:min(len(email.Headers), maxHeaders)] {
		if x.ctx.Err() != nil {
			return
		}
		if len(h.Name) > 256 || len(h.Value) > maxTotalHeaderBytes-total {
			x.issue("header_bytes_limit", "headers", i)
			fromOK, replyOK, returnOK = false, false, false
			break
		}
		total += len(h.Value)
		name := strings.ToLower(strings.TrimSpace(h.Name))
		if name != "from" && name != "reply-to" && name != "return-path" && name != "authentication-results" && name != "received-spf" {
			continue
		}
		if name == "from" {
			fromPresent = true
		}
		value, valid := unfoldedHeader(h.Value)
		if len(h.Value) > maxHeaderBytes {
			valid = false
			x.issue("header_value_limit", name, i)
		}
		if !valid {
			x.issue("malformed_header", name, i)
			switch name {
			case "from":
				fromOK = false
			case "reply-to":
				replyOK = false
			case "return-path":
				returnOK = false
			default:
				x.f.AuthenticationResults = append(x.f.AuthenticationResults, domain.PhishingAuthenticationResults{HeaderIndex: i, HeaderName: name, Raw: x.text(h.Value, name, maxHeaderBytes), Provenance: "unverified_header", Malformed: true, Methods: []domain.PhishingAuthenticationMethod{}})
			}
			continue
		}
		if name == "authentication-results" || name == "received-spf" {
			x.authentication(value, name, i)
			continue
		}
		if name == "return-path" && strings.TrimSpace(value) == "<>" {
			x.f.NullReturnPath = true
			continue
		}
		domains, parsed := mailboxDomains(mailtext.DecodeHeader(value))
		if !parsed {
			x.issue("malformed_address", name, i)
		}
		for _, d := range domains {
			_, _, puny := normalizedDomain(d)
			if puny {
				x.punycode[d] = true
			}
		}
		switch name {
		case "from":
			x.f.FromDomains = append(x.f.FromDomains, domains...)
			fromOK = fromOK && parsed
		case "reply-to":
			x.f.ReplyToDomains = append(x.f.ReplyToDomains, domains...)
			replyOK = replyOK && parsed
		case "return-path":
			x.f.ReturnPathDomains = append(x.f.ReturnPathDomains, domains...)
			returnOK = returnOK && parsed
		}
	}
	if fromPresent {
		x.f.FromDomainSource = "from_header"
	} else if email.Sender.Address != "" {
		x.f.FromDomainSource = "normalized_sender"
		sender := x.text(email.Sender.Address, "sender", maxHeaderBytes)
		domains, valid := mailboxDomains(sender)
		x.f.FromDomains = append(x.f.FromDomains, domains...)
		fromOK = fromOK && valid && len(email.Sender.Address) <= maxHeaderBytes
		if !valid {
			x.issue("malformed_address", "sender", -1)
		}
	}
	x.f.FromDomains = uniqueSorted(x.f.FromDomains)
	x.f.ReplyToDomains = uniqueSorted(x.f.ReplyToDomains)
	x.f.ReturnPathDomains = uniqueSorted(x.f.ReturnPathDomains)
	x.f.FromDomain = oneDomain(x.f.FromDomains, fromOK)
	x.f.ReplyToDomain = oneDomain(x.f.ReplyToDomains, replyOK)
	x.f.ReturnPathDomain = oneDomain(x.f.ReturnPathDomains, returnOK && !x.f.NullReturnPath)
	for _, d := range x.f.FromDomains {
		_, _, puny := normalizedDomain(d)
		if puny {
			x.punycode[d] = true
		}
	}
}
