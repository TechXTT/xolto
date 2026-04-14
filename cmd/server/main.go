package main

import (
	"context"
	"log"
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

	cfg, err := config.LoadServerConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	db, err := openServerStore(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	appCfg := &config.Config{
		Marktplaats: config.MarktplaatsConfig{},
		Scoring:     config.ScoringConfig{MinScore: 7, MarketSampleSize: 20},
		AI: config.NormalizeAIConfig(config.AIConfig{
			Enabled: cfg.AIAPIKey != "",
			BaseURL: cfg.AIBaseURL,
			APIKey:  cfg.AIAPIKey,
			Model:   cfg.AIModel,
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
	backfillMissionHunts(context.Background(), db, asst)

	srv := api.NewServer(cfg, db, asst, broker, pool, sc)
	reconcileCtx, reconcileCancel := context.WithCancel(context.Background())
	defer reconcileCancel()
	srv.StartBillingReconcileLoop(reconcileCtx, time.Hour)
	httpServer := &http.Server{
		Addr:    cfg.Address,
		Handler: srv.Handler(),
	}

	// Start server in background.
	go func() {
		log.Printf("xolto server listening on %s", cfg.Address)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	reconcileCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
}

func backfillMissionHunts(ctx context.Context, db store.Store, asst *assistant.Assistant) {
	specs, err := db.GetAllEnabledSearchConfigs()
	if err != nil {
		log.Printf("backfill: failed to load search configs: %v", err)
		return
	}
	log.Printf("backfill: scanning %d existing search configs", len(specs))
	seen := map[string]bool{}
	deployed := 0
	for _, spec := range specs {
		if spec.UserID == "" || spec.ProfileID <= 0 {
			log.Printf("backfill: skipping spec id=%d user=%q profile=%d (missing user or mission link)", spec.ID, spec.UserID, spec.ProfileID)
			continue
		}
		key := spec.UserID + "|" + strconv.FormatInt(spec.ProfileID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		mission, err := db.GetMission(spec.ProfileID)
		if err != nil {
			log.Printf("backfill: GetMission(%d) err=%v", spec.ProfileID, err)
			continue
		}
		if mission == nil {
			log.Printf("backfill: mission %d not found", spec.ProfileID)
			continue
		}
		if mission.UserID != spec.UserID {
			log.Printf("backfill: mission %d user mismatch", spec.ProfileID)
			continue
		}
		log.Printf("backfill: deploying hunts for mission=%d name=%q queries=%d", mission.ID, mission.Name, len(mission.SearchQueries))
		count, err := asst.AutoDeployHunts(ctx, spec.UserID, *mission)
		if err != nil {
			log.Printf("backfill: AutoDeployHunts mission=%d err=%v", mission.ID, err)
			continue
		}
		log.Printf("backfill: mission=%d added %d new hunts", mission.ID, count)
		deployed += count
	}
	log.Printf("backfill: scanned %d unique missions, deployed %d new hunts", len(seen), deployed)
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
