//go:build !personal_google

package config

// Standard builds never read a personal build's generated input.
func packagedGoogleJSON() []byte { return nil }
