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

	"github.com/TechXTT/xolto/internal/api"
	"github.com/TechXTT/xolto/internal/assistant"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/logging"
	"github.com/TechXTT/xolto/internal/marketplace"
	marktplaatsmp "github.com/TechXTT/xolto/internal/marketplace/marktplaats"
	"github.com/TechXTT/xolto/internal/marketplace/olxbg"
	"github.com/TechXTT/xolto/internal/marketplace/vinted"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/notify"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
	"github.com/TechXTT/xolto/internal/worker"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	logger := logging.New(os.Getenv("APP_ENV"))
	slog.SetDefault(logger)

	cfg, err := config.LoadServerConfigFromEnv()
	if err != nil {
		logger.Error("failed to load server config", "op", "server.config.load", "error", err)
		os.Exit(1)
	}
	db, err := openServerStore(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to open database store", "op", "store.open", "error", err)
		os.Exit(1)
	}
	defer db.Close()

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
	sc := scorer.New(db, appCfg.Scoring, rsn)
	asst := assistant.New(appCfg, db, provider, sc)

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

	// Backfill marketplace coverage for missions created before all-marketplace
	// auto-deploy was enabled. AutoDeployHunts is idempotent (skips dupes).
	backfillMissionHunts(context.Background(), db, asst, logger)

	srv := api.NewServer(cfg, db, asst, broker, pool, sc)
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
		logger.Info("xolto server listening", "op", "server.start", "addr", cfg.Address)
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

func openServerStore(ctx context.Context, databaseURL string) (interface {
	store.Store
	Close() error
}, error) {
	if looksLikePostgres(databaseURL) {
		return store.NewPostgres(ctx, databaseURL)
	}
	return store.New(databaseURL)
}

func looksLikePostgres(databaseURL string) bool {
	value := strings.ToLower(strings.TrimSpace(databaseURL))
	return strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://")
}
