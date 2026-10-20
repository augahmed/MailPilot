package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/ai/openai"
	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/config"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/ipc"
	"mailpilot.local/core/internal/logging"
	"mailpilot.local/core/internal/provider"
	"mailpilot.local/core/internal/provider/gmail"
	"mailpilot.local/core/internal/retrieval"
	"mailpilot.local/core/internal/risk"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
)

// Set by the sidecar build script from the desktop package version.
var version = "0.1.0-dev"

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) == 2 && os.Args[1] == "--embedding-self-test" {
		return embeddingSelfTest()
	}
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
	var authentication *auth.Manager
	var synchronization *mailsync.Service
	var organization *app.Organization
	var replies *app.ReplyService
	var intelligence *app.AIService
	var indexing *app.LocalIndex
	var searching *app.LocalSearch
	defer func() {
		if searching != nil {
			_ = searching.Close(context.Background())
		}
		if indexing != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := indexing.Close(closeCtx); err != nil {
				logger.Error("index shutdown delayed", "code", "index_shutdown")
				// Never close SQLite while a cancelled worker can still publish.
				_ = indexing.Close(context.Background())
			}
			cancel()
		}
		if intelligence != nil {
			intelligence.Close()
		}
		if replies != nil {
			replies.Close()
		}
		if synchronization != nil {
			synchronization.Clear()
		}
		if organization != nil {
			organization.Close()
		}
		if authentication != nil {
			authentication.Close()
		}
		if store != nil {
			if err := store.Close(); err != nil {
				logger.Error("database close failed", "code", "database_close")
			}
		}
	}()
	logger.Info("core started", "version", version)
	ctxRoot := ctx
	err = ipc.Serve(ctx, os.Stdin, os.Stdout, func(ctx context.Context) (ipc.HealthService, error) {
		var err error
		store, err = sqlite.Open(ctx, cfg.DataDir)
		if err != nil {
			logger.Error("database initialization failed", "code", "database_init")
			return nil, err
		}
		logger.Info("database initialized", "schema_version", store.SchemaVersion())
		tokenStore := credentials.NewKeyring(cfg.DataDir, cfg.GoogleClientID)
		authentication = auth.NewManager(ctxRoot, gmail.NewOAuth(cfg.GoogleClientID, cfg.GoogleClientSecret), tokenStore, store.Accounts(), tokenStore.Reference())
		authentication.Check()
		application := app.New(store, version)
		riskConfig := risk.DefaultConfig()
		riskConfig.High = cfg.RiskHighThreshold
		riskEngine, err := risk.NewEngine(riskConfig)
		if err != nil {
			return nil, err
		}
		application.RiskEngine = riskEngine
		application.Auth = authentication
		synchronization = mailsync.New(ctxRoot, authentication.Session, func(account domain.AccountID) mailsync.Reader { return gmail.NewProvider(account, authentication) }, store.Emails(), cfg.InitialSyncLimit)
		application.Sync = synchronization
		chunking, err := app.NewCachedChunker(store.Emails(), store.Chunks())
		if err != nil {
			return nil, err
		}
		synchronization.SetChunkProcessor(chunking.Prepare)
		synchronization.SetCachedChunkProcessor(chunking.PrepareCached)
		localProvider, err := embedding.New()
		if err != nil {
			return nil, err
		}
		indexing, err = app.NewLocalIndex(ctxRoot, authentication.Session, store.Emails(), store.Chunks(), store.Indexing(), localProvider)
		if err != nil {
			_ = localProvider.Close(ctx)
			return nil, err
		}
		indexing.EnableCategoryVectors(store.CategoryVectors())
		application.Index = indexing
		searching = app.NewLocalSearch(ctxRoot, authentication.Session, retrieval.New(store.Vectors(), localProvider), store.Indexing())
		application.Search = searching
		application.Emails = store.Emails()
		application.Categories = store.Categories()
		rules, err := classification.NewRuleBasedClassifier(classification.StarterConfig())
		if err != nil {
			return nil, err
		}
		// The deterministic engine owns the conservative direct-rule gate.
		engine := rules
		organization = app.NewOrganization(ctxRoot, authentication.Session, func(account domain.AccountID) app.OrganizationProvider {
			return gmail.NewProvider(account, authentication)
		}, store.Emails(), store.Categories(), store.Classifications(), store.Organization(), engine)
		gate := make(chan struct{}, 1)
		keys := func(account domain.AccountID) credentials.SecretStore {
			return credentials.NewAPIKeyStore(cfg.DataDir+"\x00"+string(account), "openai")
		}
		intelligence = app.NewAIService(ctxRoot, authentication.Session, store.Emails(), store.AISettings(), map[string]app.ProviderAdapter{
			"openai": {External: true, Keys: keys, New: func(account domain.AccountID, model string) (ai.AIProvider, error) {
				return openai.New(keys(account), model)
			}},
		})
		application.AI = intelligence
		replies = app.NewReplyMaintenance(ctxRoot, authentication.Session, store.Replies(), func(account domain.AccountID) provider.DraftFinder { return gmail.NewProvider(account, authentication) })
		application.Replies = replies
		organization.SetRiskEngine(riskEngine)
		organization.SetOperationGate(gate)
		synchronization.SetOperationGate(gate)
		synchronization.SetCheckpoints(store.SyncCheckpoints())
		application.Organization = organization
		application.Classification = app.NewClassificationReview(authentication.Session, engine, store.Emails(), store.Categories(), store.Classifications())
		application.Reader = app.NewCachedReader(authentication.Session, store.Emails(), application.Classification, riskEngine)
		return application, nil
	})
	if err != nil && ctx.Err() == nil {
		logger.Error("protocol stopped", "code", "ipc_failure")
		return 1
	}
	logger.Info("core stopped")
	return 0
}
