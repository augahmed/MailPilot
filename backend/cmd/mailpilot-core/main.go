package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"mailpilot.local/core/internal/ai"
	"mailpilot.local/core/internal/ai/openai"
	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/classification"
	"mailpilot.local/core/internal/config"
	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/embedding/minilm"
	"mailpilot.local/core/internal/ipc"
	"mailpilot.local/core/internal/logging"
	"mailpilot.local/core/internal/provider/gmail"
	"mailpilot.local/core/internal/risk"
	"mailpilot.local/core/internal/search"
	"mailpilot.local/core/internal/store/sqlite"
	mailsync "mailpilot.local/core/internal/sync"
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
	var authentication *auth.Manager
	var synchronization *mailsync.Service
	var organization *app.Organization
	var mailboxSearch *app.MailboxSearch
	var replies *app.ReplyService
	defer func() {
		if replies != nil {
			replies.Close()
		}
		if mailboxSearch != nil {
			mailboxSearch.Close()
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
		application.Emails = store.Emails()
		application.Categories = store.Categories()
		rules, err := classification.NewRuleBasedClassifier(classification.StarterConfig())
		if err != nil {
			return nil, err
		}
		var semantic *classification.SemanticClassifier
		var embeddings *embedding.Service
		if cfg.EmbeddingModelDir != "" {
			model, modelErr := minilm.New(ctx, cfg.EmbeddingPython, cfg.EmbeddingModelDir)
			if modelErr != nil {
				return nil, modelErr
			}
			embeddings, modelErr = embedding.NewLocalClassificationService(model, store.Emails(), store.Vectors(), store.CategoryVectors())
			if modelErr != nil {
				return nil, modelErr
			}
			semantic, modelErr = classification.NewSemanticClassifier(embeddings, classification.DefaultSemanticConfig())
			if modelErr != nil {
				return nil, modelErr
			}
		}
		engine, err := classification.NewHybridClassifier(rules, semantic, classification.DefaultHybridConfig())
		if err != nil {
			return nil, err
		}
		var searchEmbeddings search.Embeddings
		if embeddings != nil {
			searchEmbeddings = embeddings
		}
		mailboxSearch = app.NewMailboxSearch(ctxRoot, authentication.Session, search.New(store.SearchIndex(), searchEmbeddings, engine), store.Categories())
		application.Search = mailboxSearch
		organization = app.NewOrganization(ctxRoot, authentication.Session, func(account domain.AccountID) app.OrganizationProvider {
			return gmail.NewProvider(account, authentication)
		}, store.Emails(), store.Categories(), store.Classifications(), store.Organization(), engine)
		gate := make(chan struct{}, 1)
		keys := func(account domain.AccountID) credentials.SecretStore {
			return credentials.NewAPIKeyStore(cfg.DataDir+"\x00"+string(account), "openai")
		}
		replies = app.NewReplyService(ctxRoot, authentication.Session, store.Replies(), store.Emails(), func(account domain.AccountID) app.ReplyProvider { return gmail.NewProvider(account, authentication) }, keys, func(account domain.AccountID, model string) (ai.AIProvider, ai.Identity, error) {
			p, err := openai.New(keys(account), model)
			if err != nil {
				return nil, ai.Identity{}, err
			}
			return p, p.Identity(), nil
		})
		replies.SetOperationGate(gate)
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
