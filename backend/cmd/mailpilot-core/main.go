package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/config"
	"mailpilot.local/core/internal/ipc"
	"mailpilot.local/core/internal/logging"
	"mailpilot.local/core/internal/store/sqlite"
)

// Set by the sidecar build script from the desktop package version.
var version = "0.1.0-dev"

func main() { os.Exit(run()) }

func run() int {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	base, err := os.UserConfigDir()
	if err != nil {
		logger.Error("configuration unavailable", "code", "config_default")
		return 1
	}
	if runtime.GOOS == "linux" {
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return 1
			}
			base = filepath.Join(home, ".local", "share")
		}
	}
	cfg, err := config.Load(os.Args[1:], os.Getenv, filepath.Join(base, "com.mailpilot.desktop"))
	if err != nil {
		logger.Error("invalid configuration", "code", "config_invalid")
		return 1
	}
	logger = logging.New(os.Stderr, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() { <-ctx.Done(); _ = os.Stdin.Close() }()
	var store *sqlite.Store
	defer func() {
		if store != nil {
			if err := store.Close(); err != nil {
				logger.Error("database close failed", "code", "database_close")
			}
		}
	}()
	logger.Info("core started", "version", version)
	err = ipc.Serve(ctx, os.Stdin, os.Stdout, func(ctx context.Context) (ipc.HealthService, error) {
		var err error
		store, err = sqlite.Open(ctx, cfg.DataDir)
		if err != nil {
			logger.Error("database initialization failed", "code", "database_init")
			return nil, err
		}
		logger.Info("database initialized", "schema_version", store.SchemaVersion())
		return app.New(store, version), nil
	})
	if err != nil && ctx.Err() == nil {
		logger.Error("protocol stopped", "code", "ipc_failure")
		return 1
	}
	logger.Info("core stopped")
	return 0
}
