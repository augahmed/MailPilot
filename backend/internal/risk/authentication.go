package risk

import (
	"strings"

	"mailpilot.local/core/internal/domain"
)

type authToken struct {
	value  string
	quoted bool
}

// This bounded lexer retains semicolons inside quoted values, ignores nested
// comments, and rejects unfinished quotes/comments instead of finding bogus
// method assignments inside attacker-controlled prose.
func authTokens(value string) ([]authToken, bool) {
	tokens := []authToken{}
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, authToken{value: word.String()})
			word.Reset()
		}
	}
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '\t':
			flush()
		case ';', '=':
			flush()
			tokens = append(tokens, authToken{value: string(value[i])})
		case '(':
			flush()
			depth := 1
			for depth > 0 {
				i++
				if i >= len(value) {
					return tokens, false
				}
				switch value[i] {
				case '\\':
					i++
					if i >= len(value) {
						return tokens, false
					}
				case '(':
					depth++
					if depth > 32 {
						return tokens, false
					}
				case ')':
					depth--
				}
			}
		case ')':
			return tokens, false
		case '"':
			if word.Len() > 0 {
				return tokens, false
			}
			var quoted strings.Builder
			closed := false
			for i++; i < len(value); i++ {
				if value[i] == '"' {
					closed = true
					break
				}
				if value[i] == '\\' {
					i++
					if i >= len(value) {
						return tokens, false
					}
				}
				quoted.WriteByte(value[i])
			}
			if !closed {
				return tokens, false
			}
			tokens = append(tokens, authToken{value: quoted.String(), quoted: true})
		default:
			if value[i] < 32 || value[i] == 127 {
				return tokens, false
			}
			word.WriteByte(value[i])
		}
		if len(tokens) > 1024 {
			return tokens, false
		}
	}
	flush()
	return tokens, true
}
func keyword(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func digits(value string) bool {
	if value == "" || len(value) > 8 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Whitespace may surround the separator, but cannot split an identifier.
func joinedAuthName(tokens []authToken, separator string) (string, bool) {
	if len(tokens) < 1 || len(tokens) > 3 {
		return "", false
	}
	var name strings.Builder
	for i, t := range tokens {
		if t.quoted || i > 0 && !strings.HasSuffix(tokens[i-1].value, separator) && !strings.HasPrefix(t.value, separator) {
			return "", false
		}
		name.WriteString(t.value)
	}
	return name.String(), true
}
func authMethod(tokens []authToken) (domain.PhishingAuthenticationMethod, bool) {
	method := domain.PhishingAuthenticationMethod{Properties: []domain.PhishingAuthenticationProperty{}}
	equal := -1
	for i, t := range tokens {
		if !t.quoted && t.value == "=" {
			equal = i
			break
		}
	}
	if equal < 1 || equal+1 >= len(tokens) || tokens[equal+1].quoted {
		return method, false
	}
	name, validName := joinedAuthName(tokens[:equal], "/")
	if !validName {
		return method, false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 || !keyword(parts[0]) || len(parts) == 2 && !digits(parts[1]) {
		return method, false
	}
	method.Method = strings.ToLower(parts[0])
	method.Version = "1"
	if len(parts) == 2 {
		method.Version = parts[1]
	}
	method.Result = strings.ToLower(tokens[equal+1].value)
	if !keyword(method.Result) {
		return method, false
	}
	properties, reason, valid := authProperties(tokens[equal+2:], false)
	method.Properties = properties
	method.Reason = reason
	return method, valid
}
func authProperties(tokens []authToken, spf bool) ([]domain.PhishingAuthenticationProperty, string, bool) {
	properties := []domain.PhishingAuthenticationProperty{}
	reason := ""
	for len(tokens) > 0 {
		if spf && tokens[0].value == ";" && !tokens[0].quoted {
			tokens = tokens[1:]
			continue
		}
		eq := -1
		for i, t := range tokens {
			if !t.quoted && t.value == "=" {
				eq = i
				break
			}
			if i > 3 {
				break
			}
		}
		if eq < 1 || eq+1 >= len(tokens) {
			return properties, reason, false
		}
		key, validKey := joinedAuthName(tokens[:eq], ".")
		if !validKey {
			return properties, reason, false
		}
		key = strings.ToLower(key)
		value := tokens[eq+1]
		if !value.quoted && (value.value == "=" || value.value == ";") {
			return properties, reason, false
		}
		if key == "reason" {
			reason = value.value
		} else {
			parts := strings.Split(key, ".")
			if spf && len(parts) == 1 {
				parts = []string{"spf", key}
			}
			if len(parts) != 2 || !keyword(parts[0]) || !keyword(parts[1]) {
				return properties, reason, false
			}
			properties = append(properties, domain.PhishingAuthenticationProperty{Type: parts[0], Name: parts[1], Value: value.value})
		}
		tokens = tokens[eq+2:]
		if len(properties) > 50 {
			return properties, reason, false
		}
	}
	return properties, reason, true
}
func (x *featureExtraction) authentication(value, name string, index int) {
	data := domain.PhishingAuthenticationResults{HeaderIndex: index, HeaderName: name, Raw: value, Version: "1", Provenance: "unverified_header", Methods: []domain.PhishingAuthenticationMethod{}}
	tokens, valid := authTokens(value)
	if name == "received-spf" {
		if len(tokens) == 0 || tokens[0].quoted || !keyword(tokens[0].value) {
			valid = false
		} else {
			props, reason, ok := authProperties(tokens[1:], true)
			valid = valid && ok
			data.Methods = append(data.Methods, domain.PhishingAuthenticationMethod{Method: "spf", Version: "1", Result: strings.ToLower(tokens[0].value), Reason: reason, Properties: props})
		}
	} else {
		clauses := [][]authToken{{}}
		for _, token := range tokens {
			if token.value == ";" && !token.quoted {
				clauses = append(clauses, []authToken{})
			} else {
				clauses[len(clauses)-1] = append(clauses[len(clauses)-1], token)
			}
		}
		if len(clauses) < 2 || len(clauses) > 65 || len(clauses[0]) < 1 || len(clauses[0]) > 2 {
			valid = false
		} else {
			data.AuthservID = clauses[0][0].value
			if data.AuthservID == "" || strings.ContainsAny(data.AuthservID, "=;") {
				valid = false
			}
			if len(clauses[0]) == 2 {
				data.Version = clauses[0][1].value
				if !digits(data.Version) {
					valid = false
				}
			}
			for _, clause := range clauses[1:] {
				if len(clause) == 1 && strings.EqualFold(clause[0].value, "none") && !clause[0].quoted {
					data.NoResults = true
					if len(clauses) != 2 {
						valid = false
					}
					continue
				}
				method, ok := authMethod(clause)
				valid = valid && ok
				if method.Method != "" {
					data.Methods = append(data.Methods, method)
				}
			}
		}
	}
	data.Malformed = !valid
	if !valid {
		x.issue("malformed_authentication", name, index)
	} else if data.Version != "1" {
		x.issue("unsupported_authentication_version", name, index)
	} else {
		for _, method := range data.Methods {
			if method.Version != "1" {
				x.issue("unsupported_authentication_version", name, index)
				continue
			}
			if !knownAuthResult(method.Method, method.Result) {
				if method.Method == "spf" || method.Method == "dkim" || method.Method == "dmarc" {
					x.issue("unknown_authentication_result", name, index)
				}
				continue
			}
			switch method.Method {
			case "spf":
				x.f.SPFResults = append(x.f.SPFResults, method.Result)
			case "dkim":
				x.f.DKIMResults = append(x.f.DKIMResults, method.Result)
			case "dmarc":
				x.f.DMARCResults = append(x.f.DMARCResults, method.Result)
			}
		}
	}
	x.f.AuthenticationResults = append(x.f.AuthenticationResults, data)
}
func knownAuthResult(method, result string) bool {
	switch result {
	case "pass", "fail", "none", "temperror", "permerror":
		return true
	case "softfail":
		return method == "spf"
	case "neutral", "policy":
		return method == "spf" || method == "dkim"
	}
	return false
}
func authSummary(results []string) string {
	if len(results) == 0 {
		return "unknown"
	}
	if len(results) > 1 {
		return "conflicting"
	}
	return results[0]
}
