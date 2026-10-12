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
	EmbeddingPython    string
	EmbeddingModelDir  string
	RiskHighThreshold  int
	InitialSyncLimit   int
	GoogleClientID     string
	GoogleClientSecret credentials.Secret
	DataDir            string
	LogLevel           slog.Level
}

// Load gives explicit flags precedence over environment values and defaults.
// defaultDir is supplied by the composition root, keeping this function testable.
func Load(args []string, getenv func(string) string, defaultDir string) (Config, error) {
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
	python := getenv("MAILPILOT_EMBEDDING_PYTHON")
	modelDir := getenv("MAILPILOT_EMBEDDING_MODEL_DIR")
	dir := defaultDir
	if value := getenv("MAILPILOT_DATA_DIR"); value != "" {
		dir = value
	}
	level := "info"
	if value := getenv("MAILPILOT_LOG_LEVEL"); value != "" {
		level = value
	}
	flags := flag.NewFlagSet("mailpilot-core", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&python, "embedding-python", python, "absolute trusted local inference Python executable")
	flags.StringVar(&modelDir, "embedding-model-dir", modelDir, "absolute installed MiniLM model directory")
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
	if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return Config{}, errors.New("data directory must be an absolute non-root path")
	}
	levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	parsed, ok := levels[strings.ToLower(level)]
	if !ok {
		return Config{}, errors.New("invalid log level")
	}
	if (python == "") != (modelDir == "") || (python != "" && (!filepath.IsAbs(python) || !filepath.IsAbs(modelDir))) {
		return Config{}, errors.New("embedding paths must be paired and absolute")
	}
	clientID := strings.TrimSpace(getenv("MAILPILOT_GOOGLE_CLIENT_ID"))
	if clientID != "" && (!strings.HasSuffix(clientID, ".apps.googleusercontent.com") || strings.ContainsAny(clientID, " /\r\n")) {
		return Config{}, errors.New("invalid Google client configuration")
	}
	return Config{EmbeddingPython: python, EmbeddingModelDir: modelDir, RiskHighThreshold: high, InitialSyncLimit: limit, DataDir: filepath.Clean(dir), LogLevel: parsed, GoogleClientID: clientID, GoogleClientSecret: credentials.NewSecret(getenv("MAILPILOT_GOOGLE_CLIENT_SECRET"))}, nil
}
