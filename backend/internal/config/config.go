// Package config loads runtime configuration, including redacted desktop OAuth
// client configuration. User tokens are never read from environment variables.
package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"mailpilot.local/core/internal/credentials"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Demo               bool
	ResetDemo          bool
	RiskHighThreshold  int
	InitialSyncLimit   int
	GoogleClientID     string
	GoogleClientSecret credentials.Secret
	DataDir            string
	LogLevel           slog.Level
}

// Load gives explicit flags precedence over environment values and defaults.
// defaultDir is supplied by the composition root, keeping this function testable.
func Load(args []string, lookup func(string) (string, bool), defaultDir string) (Config, error) {
	getenv := func(key string) string { value, _ := lookup(key); return value }
	high := 60
	if value := getenv("MAILPILOT_RISK_HIGH_THRESHOLD"); value != "" {
		var err error
		high, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("invalid risk threshold")
		}
	}
	limit := 500
	if value := getenv("MAILPILOT_SYNC_LIMIT"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("invalid sync limit")
		}
	}
	dir := defaultDir
	if value := getenv("MAILPILOT_DATA_DIR"); value != "" {
		dir = value
	}
	level := "info"
	if value := getenv("MAILPILOT_LOG_LEVEL"); value != "" {
		level = value
	}
	flags := flag.NewFlagSet("mailpilot-core", flag.ContinueOnError)
	var demo, resetDemo bool
	flags.BoolVar(&demo, "demo", false, "isolated offline synthetic demo")
	flags.BoolVar(&resetDemo, "reset-demo", false, "restore bundled demo samples (requires --demo)")
	flags.SetOutput(io.Discard)
	flags.IntVar(&high, "risk-high-threshold", high, "HIGH phishing-risk threshold (31-100)")
	flags.IntVar(&limit, "sync-limit", limit, "maximum messages per Inbox sync (1-10000)")
	flags.StringVar(&dir, "data-dir", dir, "absolute private application data directory")
	flags.StringVar(&level, "log-level", level, "debug, info, warn, or error")
	if err := flags.Parse(args); err != nil {
		return Config{}, errors.New("invalid configuration flags")
	}
	if high < 31 || high > 100 {
		return Config{}, errors.New("risk threshold must be between 31 and 100")
	}
	if limit < 1 || limit > 10000 {
		return Config{}, errors.New("sync limit must be between 1 and 10000")
	}
	if flags.NArg() != 0 {
		return Config{}, errors.New("unexpected positional arguments")
	}
	if resetDemo && !demo {
		return Config{}, errors.New("demo reset requires demo mode")
	}
	explicitDir := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "data-dir" {
			explicitDir = true
		}
	})
	if demo {
		if explicitDir {
			return Config{}, errors.New("demo profile cannot be selected by a data path")
		}
		dir = defaultDir + ".demo-v1"
	} else if filepath.Clean(dir) == filepath.Clean(defaultDir+".demo-v1") {
		return Config{}, errors.New("demo profile requires demo mode")
	}
	if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return Config{}, errors.New("data directory must be an absolute non-root path")
	}
	levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	parsed, ok := levels[strings.ToLower(level)]
	if !ok {
		return Config{}, errors.New("invalid log level")
	}
	var clientID string
	var clientSecret credentials.Secret
	var err error
	if !demo {
		clientID, clientSecret, err = googleConfiguration(lookup, packagedGoogleJSON())
	}
	if err != nil {
		return Config{}, err
	}
	return Config{Demo: demo, ResetDemo: resetDemo, RiskHighThreshold: high, InitialSyncLimit: limit, DataDir: filepath.Clean(dir), LogLevel: parsed, GoogleClientID: clientID, GoogleClientSecret: clientSecret}, nil
}
