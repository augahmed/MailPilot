//go:build personal_google

package config

import _ "embed"

// Generated temporarily by scripts/personal.mjs, before compilation/signing.
// This contains public desktop-client configuration, never user credentials.
//
//go:embed personal-google-client.json
var personalGoogleJSON []byte

func packagedGoogleJSON() []byte {
	if len(personalGoogleJSON) == 0 {
		return []byte{}
	}
	return personalGoogleJSON
}
