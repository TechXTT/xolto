package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/api"
	"github.com/TechXTT/xolto/internal/assistant"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/linear"
	"github.com/TechXTT/xolto/internal/logging"
	"github.com/TechXTT/xolto/internal/marketplace"
	marktplaatsmp "github.com/TechXTT/xolto/internal/marketplace/marktplaats"
	"github.com/TechXTT/xolto/internal/marketplace/olxbg"
	"github.com/TechXTT/xolto/internal/marketplace/vinted"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/notify"
	"github.com/TechXTT/xolto/internal/observability"
	"github.com/TechXTT/xolto/internal/outreach"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
	"github.com/TechXTT/xolto/internal/support"
	"github.com/TechXTT/xolto/internal/worker"
	"github.com/joho/godotenv"
)

// release is injected at build time via:
//
//	go build -ldflags "-X github.com/TechXTT/xolto/cmd/server/main.release=$(git rev-parse --short HEAD)"
//
// When not injected (local / CI without ldflags), Init falls back to the
// SENTRY_RELEASE environment variable, which Railway can set without a rebuild.
var release string

func main() {
	_ = godotenv.Load()
	logger := logging.New(os.Getenv("APP_ENV"))
	slog.SetDefault(logger)

	// Initialise Sentry error tracking. No-op when SENTRY_DSN is unset.
	observability.Init(release)
	defer observability.Flush(2 * time.Second)

	cfg, err := config.LoadServerConfigFromEnv()
	if err != nil {
		logger.Error("failed to load server config", "op", "server.config.load", "error", err)
		os.Exit(1)
	}

	// W19-23 Phase 1: install the global AI-spend tracker. Founder-locked
	// $3/24h cap (Decision Log 2026-04-27). Every AI_API_KEY-routed call
	// site (scorer, reasoner, replycopilot, assistant, generator, support
	// classifier, must-have evaluator) reads aibudget.Global() for the
	// pre-spend gate.
	aibudget.SetGlobal(aibudget.New())
	logger.Info("ai budget tracker initialised",
		"op", "aibudget.init",
		"cap_usd", aibudget.DefaultCapUSD,
		"window", aibudget.Window.String(),
	)
	dbPoolCfg := store.NormalizeDBPoolConfig(store.DBPoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
	})
	db, err := openServerStore(context.Background(), cfg.DatabaseURL, dbPoolCfg)
	if err != nil {
		logger.Error("failed to open database store", "op", "store.open", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// W19-24 — fail-loud at deploy time when load-bearing W19-23 wiring is
	// broken. The 2026-04-27 incident surfaced two silent failure modes:
	// (a) the global aibudget.Tracker singleton was non-nil (it works) but
	// (b) the inline CREATE TABLE for ai_budget_overrides errored at startup
	// and was swallowed by `_, _ = db.ExecContext(...)`, leaving production
	// running with broken audit logging until a synthetic test caught it
	// hours later. The assertions below convert that class into a deploy-time
	// crash so Railway's health-gate refuses to promote the bad container
	// instead of accepting traffic against half-wired state.
	if aibudget.Global() == nil {
		logger.Error("aibudget global tracker is nil after init — the W19-23 cap is not wired",
			"op", "aibudget.assert")
		os.Exit(1)
	}
	if err := db.AIBudgetTableReady(context.Background()); err != nil {
		logger.Error(
			"ai_budget_overrides table is not ready — the W19-23 audit-log migration did not apply. "+
				"Inspect store.migratePostgresCalibration and migrations/000016_ai_budget_overrides.up.sql; "+
				"production cannot accept traffic without audit-log persistence.",
			"op", "aibudget.assert.audit_table",
			"error", err,
		)
		os.Exit(1)
	}
	logger.Info("ai budget wiring verified", "op", "aibudget.assert.ok")

	appCfg := &config.Config{
		Marktplaats: config.MarktplaatsConfig{},
		Scoring:     config.ScoringConfig{MinScore: 7, MarketSampleSize: 20},
		AI: config.NormalizeAIConfig(config.AIConfig{
			Enabled:       cfg.AIAPIKey != "",
			BaseURL:       cfg.AIBaseURL,
			APIKey:        cfg.AIAPIKey,
			Model:         cfg.AIModel,
			PromptVersion: cfg.AIPromptVersion,
		}),
	}
	provider := marktplaatsmp.New(appCfg.Marktplaats)
	rsn := reasoner.New(appCfg.AI)
	rsn.SetModel(cfg.AIModelScorer) // XOL-60 SUP-9: per-call-site model override
	sc := scorer.New(db, appCfg.Scoring, rsn)
	asst := assistant.New(appCfg, db, provider, sc)
	asst.SetModels(cfg.AIModelAssistantBrief, cfg.AIModelAssistantDraft, cfg.AIModelAssistantChat) // XOL-60 SUP-9

	// Wire AI usage tracking: each module reports token counts via a callback,
	// and we persist them to the ai_usage_log table.
	usageCB := func(userID string, missionID int64, callType, model string, prompt, completion, latencyMs int, success bool, errMsg string) {
		_ = db.RecordAIUsage(models.AIUsageEntry{
			UserID:           strings.TrimSpace(userID),
			MissionID:        missionID,
			CallType:         callType,
			Model:            model,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			LatencyMs:        latencyMs,
			Success:          success,
			ErrorMsg:         errMsg,
		})
	}
	rsn.SetUsageCallback(usageCB)
	asst.SetUsageCallback(usageCB)

	// Construct the must-have semantic evaluator (XOL-22). When AI_API_KEY is
	// empty the evaluator is nil and the /matches handler falls back to the
	// tokenizer-only path automatically.
	mustHaveEvaluator := reasoner.NewMustHaveEvaluatorLLM(reasoner.MustHaveEvaluatorConfig{
		APIKey:                    cfg.AIAPIKey,
		BaseURL:                   cfg.AIBaseURL,
		Model:                     cfg.AIModelMustHave,
		MaxCallsPerMissionPerHour: cfg.AIMaxMustHaveCallsPerMissionPerHour,
		Store:                     db,
		UsageCallback:             usageCB,
	})
	logger.Info("musthave evaluator initialised",
		"op", "musthave.evaluator.init",
		"configured", mustHaveEvaluator != nil,
	)
	broker := api.NewSSEBroker()
	dispatcher := notify.NewSSEDispatcher(broker)
	registry := marketplace.NewRegistry()
	registry.Register(provider)
	registry.Register(vinted.New(vinted.NetherlandsConfig()))
	registry.Register(vinted.New(vinted.DenmarkConfig()))
	registry.Register(olxbg.New())
	pool := worker.NewPool(db, registry, sc, dispatcher, appCfg.Scoring.MinScore, 2*time.Minute)
	emailNotifier := notify.NewEmail(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	pool.SetEmailNotifier(emailNotifier)
	pool.Start(context.Background())
	defer pool.Stop()

	// Construct SMSEscalator (XOL-56 SUP-5). SUP-4's classifier will consume
	// this as a callback. In non-prod envs Twilio vars may be absent; the
	// escalator handles dry-run logging in that case.
	var twilioSender support.TwilioSenderInterface
	if cfg.TwilioAccountSID != "" {
		twilioSender = support.NewTwilioClient(cfg.TwilioAccountSID, cfg.TwilioAuthToken, nil)
	}
	smsEscalator := support.NewSMSEscalator(support.SMSEscalatorConfig{
		Sender:     twilioSender,
		FromNumber: cfg.TwilioFromNumber,
		FounderNum: cfg.FounderSMSNumber,
		AppEnv:     cfg.AppEnv,
	})

	// Start the outreach stale-transition goroutine. Wakes every hour and
	// transitions awaiting_reply threads older than 7 days to stale.
	outreachCtx, outreachCancel := context.WithCancel(context.Background())
	defer outreachCancel()
	outreach.StartStaleTransitionScheduler(outreachCtx, db, time.Hour, 7*24*time.Hour)

	// Backfill marketplace coverage for missions created before all-marketplace
	// auto-deploy was enabled. AutoDeployHunts is idempotent (skips dupes).
	backfillMissionHunts(context.Background(), db, asst, logger)

	srv := api.NewServer(cfg, db, asst, broker, pool, sc)
	srv.SetMustHaveEvaluator(mustHaveEvaluator) // XOL-22: semantic must-have evaluator

	// Start the support classifier worker pool (XOL-59 SUP-8, MCP retired SUP-10).
	// Workers consume from the Plain webhook channel, classify events, and
	// attach Plain labels + Linear issues + draft notes.
	// All Plain calls route through the GraphQL client (PLAIN_API_KEY).
	// Model is resolved from AI_MODEL_CLASSIFIER → AI_MODEL → default (gpt-4o-mini).
	classifierCtx, classifierCancel := context.WithCancel(context.Background())
	defer classifierCancel()
	plainGQLClient := plain.New(cfg.PlainAPIKey)

	// Boot preflight — probe the GraphQL endpoint to verify API key health.
	// Server must continue to boot even when preflight fails.
	{
		pfCtx, pfCancel := context.WithTimeout(context.Background(), 5*time.Second)
		pf := plainGQLClient.Preflight(pfCtx)
		pfCancel()

		pfAttrs := []any{
			"op", "plain.preflight",
			"configured", pf.Configured,
			"endpoint", pf.Endpoint,
			"key_len", pf.KeyLen,
			"raw_key_len", pf.RawKeyLen,
			"status_code", pf.StatusCode,
			"body_snippet", pf.BodySnippet,
		}
		if pf.Err != nil {
			pfAttrs = append(pfAttrs, "error", pf.Err)
		}
		if pf.Configured && pf.StatusCode >= 200 && pf.StatusCode < 300 {
			logger.Info("plain API preflight OK", pfAttrs...)
		} else {
			logger.Warn("plain API preflight failed", pfAttrs...)
		}

		if pf.RawKeyLen != pf.KeyLen {
			logger.Warn(
				"PLAIN_API_KEY has surrounding whitespace — trim before setting env var",
				"op", "plain.key_whitespace_detected",
				"key_len", pf.KeyLen,
				"raw_key_len", pf.RawKeyLen,
			)
		}
	}

	plainSupportAdapter := plain.NewSupportAdapter(plainGQLClient)
	linearMCPClient := linear.NewLinearMCPClient(cfg.LinearAPIKey)
	classifierLLMClient := support.NewOpenAICompatClient(cfg.AIAPIKey, cfg.AIBaseURL)
	classifierWorker := support.NewClassifierWorker(support.ClassifierConfig{
		Store:       db,
		PlainAPI:    plainSupportAdapter,
		LinearMCP:   linearMCPClient,
		LLM:         classifierLLMClient,
		LLMModel:    cfg.AIModelClassifier,
		SMSCallback: smsEscalator.NotifyIncident,
		AppEnv:      cfg.AppEnv,
	})
	classifierWorker.Start(classifierCtx, srv.SupportEvents(), cfg.SupportClassifierWorkers)
	reconcileCtx, reconcileCancel := context.WithCancel(context.Background())
	defer reconcileCancel()
	srv.StartBillingReconcileLoop(reconcileCtx, time.Hour)
	httpServer := &http.Server{
		Addr:              cfg.Address,
		Handler:           srv.Handler(),
		ReadTimeout:       cfg.HTTPTimeouts.ReadTimeout,
		WriteTimeout:      cfg.HTTPTimeouts.WriteTimeout,
		IdleTimeout:       cfg.HTTPTimeouts.IdleTimeout,
		ReadHeaderTimeout: cfg.HTTPTimeouts.ReadHeaderTimeout,
	}

	// Start server in background.
	go func() {
		logger.Info(
			"xolto server listening",
			"op", "server.start",
			"addr", cfg.Address,
			"db_max_open_conns", dbPoolCfg.MaxOpenConns,
			"db_max_idle_conns", dbPoolCfg.MaxIdleConns,
			"db_conn_max_lifetime", dbPoolCfg.ConnMaxLifetime.String(),
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server listen failed", "op", "server.listen", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down", "op", "server.shutdown.start")
	reconcileCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "op", "server.shutdown", "error", err)
		return
	}
	logger.Info("server shutdown complete", "op", "server.shutdown.complete")
}

func backfillMissionHunts(ctx context.Context, db store.Store, asst *assistant.Assistant, logger *slog.Logger) {
	specs, err := db.GetAllEnabledSearchConfigs()
	if err != nil {
		logger.Error("backfill failed to load search configs", "op", "backfill.load_specs", "error", err)
		return
	}
	logger.Info("backfill scanning existing search configs", "op", "backfill.scan.start", "spec_count", len(specs))
	seen := map[string]bool{}
	deployed := 0
	for _, spec := range specs {
		if spec.UserID == "" || spec.ProfileID <= 0 {
			logger.Warn("backfill skipping invalid search config", "op", "backfill.scan.skip_invalid_spec", "search_id", spec.ID, "user_id", spec.UserID, "mission_id", spec.ProfileID)
			continue
		}
		key := spec.UserID + "|" + strconv.FormatInt(spec.ProfileID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		mission, err := db.GetMission(spec.ProfileID)
		if err != nil {
			logger.Error("backfill mission lookup failed", "op", "backfill.mission.get", "mission_id", spec.ProfileID, "error", err)
			continue
		}
		if mission == nil {
			logger.Warn("backfill mission not found", "op", "backfill.mission.missing", "mission_id", spec.ProfileID)
			continue
		}
		if mission.UserID != spec.UserID {
			logger.Warn("backfill mission user mismatch", "op", "backfill.mission.user_mismatch", "mission_id", spec.ProfileID, "mission_user_id", mission.UserID, "search_user_id", spec.UserID)
			continue
		}
		logger.Info("backfill deploying hunts", "op", "backfill.deploy.start", "mission_id", mission.ID, "mission_name", mission.Name, "query_count", len(mission.SearchQueries))
		count, err := asst.AutoDeployHunts(ctx, spec.UserID, *mission)
		if err != nil {
			logger.Error("backfill auto deploy failed", "op", "backfill.deploy", "mission_id", mission.ID, "error", err)
			continue
		}
		logger.Info("backfill deployed hunts", "op", "backfill.deploy.success", "mission_id", mission.ID, "deployed_count", count)
		deployed += count
	}
	logger.Info("backfill completed", "op", "backfill.scan.complete", "mission_count", len(seen), "deployed_count", deployed)
}

func openServerStore(ctx context.Context, databaseURL string, poolCfg store.DBPoolConfig) (interface {
	store.Store
	Close() error
}, error) {
	if looksLikePostgres(databaseURL) {
		return store.NewPostgresWithPool(ctx, databaseURL, poolCfg)
	}
	return store.NewWithPool(databaseURL, poolCfg)
}

func looksLikePostgres(databaseURL string) bool {
	value := strings.ToLower(strings.TrimSpace(databaseURL))
	return strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://")
}
