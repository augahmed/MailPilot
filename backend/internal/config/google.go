package config

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"mailpilot.local/core/internal/credentials"
)

var errGoogleConfig = errors.New("invalid Google desktop client configuration")
var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\.apps\.googleusercontent\.com$`)
var clientSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validGoogleClient(id, secret string) bool {
	return len(id) <= 256 && clientIDPattern.MatchString(id) && len(secret) > 0 && len(secret) <= 256 && clientSecretPattern.MatchString(secret) && !strings.HasPrefix(secret, "sk-") && !strings.HasPrefix(secret, "AIza")
}

// The build resource has exactly two fields. Downloaded URLs, project metadata,
// tokens and unrelated keys cannot become runtime configuration.
func googleConfiguration(lookup func(string) (string, bool), packaged []byte) (string, credentials.Secret, error) {
	var id, secret string
	if packaged != nil {
		var fields map[string]json.RawMessage
		if len(packaged) > 4096 || json.Unmarshal(packaged, &fields) != nil || len(fields) != 2 || json.Unmarshal(fields["client_id"], &id) != nil || json.Unmarshal(fields["client_secret"], &secret) != nil || !validGoogleClient(id, secret) {
			return "", credentials.Secret{}, errGoogleConfig
		}
	}
	// An explicit pair overrides the default as a unit. Two empty values disable
	// OAuth for isolated smoke checks; partial overrides never mix two clients.
	envID, hasID := lookup("MAILPILOT_GOOGLE_CLIENT_ID")
	envSecret, hasSecret := lookup("MAILPILOT_GOOGLE_CLIENT_SECRET")
	if hasID || hasSecret {
		if !hasID || !hasSecret || (envID != "" || envSecret != "") && !validGoogleClient(envID, envSecret) {
			return "", credentials.Secret{}, errGoogleConfig
		}
		id, secret = envID, envSecret
	}
	return id, credentials.NewSecret(secret), nil
}
