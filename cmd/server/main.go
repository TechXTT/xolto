package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/api"
	"github.com/TechXTT/marktbot/internal/assistant"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/marketplace"
	marktplaatsmp "github.com/TechXTT/marktbot/internal/marketplace/marktplaats"
	"github.com/TechXTT/marktbot/internal/marketplace/vinted"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/reasoner"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
	"github.com/TechXTT/marktbot/internal/worker"
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
		AI: config.AIConfig{
			Enabled: cfg.AIAPIKey != "",
			BaseURL: cfg.AIBaseURL,
			APIKey:  cfg.AIAPIKey,
			Model:   cfg.AIModel,
		},
	}
	provider := marktplaatsmp.New(appCfg.Marktplaats)
	rsn := reasoner.New(appCfg.AI)
	sc := scorer.New(db, appCfg.Scoring, rsn)
	asst := assistant.New(appCfg, db, provider, sc)
	broker := api.NewSSEBroker()
	dispatcher := notify.NewSSEDispatcher(broker)
	registry := marketplace.NewRegistry()
	registry.Register(provider)
	registry.Register(vinted.New())
	pool := worker.NewPool(db, registry, sc, dispatcher, appCfg.Scoring.MinScore, 2*time.Minute)
	pool.Start(context.Background())
	defer pool.Stop()
	server := api.NewServer(cfg, db, asst, broker, pool)
	log.Printf("marktbot server listening on %s", cfg.Address)
	log.Fatal(server.ListenAndServe())
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
