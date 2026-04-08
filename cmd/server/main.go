package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TechXTT/marktbot/internal/api"
	"github.com/TechXTT/marktbot/internal/assistant"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/marketplace"
	marktplaatsmp "github.com/TechXTT/marktbot/internal/marketplace/marktplaats"
	"github.com/TechXTT/marktbot/internal/marketplace/olxbg"
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
	registry.Register(olxbg.New())
	pool := worker.NewPool(db, registry, sc, dispatcher, appCfg.Scoring.MinScore, 2*time.Minute)
	emailNotifier := notify.NewEmail(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	pool.SetEmailNotifier(emailNotifier)
	pool.Start(context.Background())
	defer pool.Stop()

	srv := api.NewServer(cfg, db, asst, broker, pool)
	httpServer := &http.Server{
		Addr:    cfg.Address,
		Handler: srv.Handler(),
	}

	// Start server in background.
	go func() {
		log.Printf("marktbot server listening on %s", cfg.Address)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
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
