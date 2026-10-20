//go:build !darwin || !arm64 || !cgo

package embedding

import "context"

// Non-native builds keep the rest of MailPilot usable, without a cloud fallback.
func loadNative(context.Context) (engine, error) { return nil, ErrUnavailable }
