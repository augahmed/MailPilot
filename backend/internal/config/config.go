// Package config loads non-secret runtime configuration. It never reads .env files.
package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
)

type Config struct {
	DataDir  string
	LogLevel slog.Level
}

// Load gives explicit flags precedence over environment values and defaults.
// defaultDir is supplied by the composition root, keeping this function testable.
func Load(args []string, getenv func(string) string, defaultDir string) (Config, error) {
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
	flags.StringVar(&dir, "data-dir", dir, "absolute private application data directory")
	flags.StringVar(&level, "log-level", level, "debug, info, warn, or error")
	if err := flags.Parse(args); err != nil {
		return Config{}, errors.New("invalid configuration flags")
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
	return Config{DataDir: filepath.Clean(dir), LogLevel: parsed}, nil
}
