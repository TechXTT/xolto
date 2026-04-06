package cliapp

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/TechXTT/marktbot/internal/assistant"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/discordbot"
	"github.com/TechXTT/marktbot/internal/generator"
	"github.com/TechXTT/marktbot/internal/marketplace"
	marktplaatsmp "github.com/TechXTT/marktbot/internal/marketplace/marktplaats"
	"github.com/TechXTT/marktbot/internal/messenger"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/reasoner"
	"github.com/TechXTT/marktbot/internal/scheduler"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

var (
	configPath       = flag.String("config", "config.yaml", "path to config file")
	once             = flag.Bool("once", false, "run one cycle and exit")
	dryRun           = flag.Bool("dry-run", false, "search and score but don't send messages")
	verbose          = flag.Bool("verbose", false, "enable debug logging")
	generateSearches = flag.String("generate-searches", "", "generate YAML search items for a topic and exit")
	cleanup          = flag.String("cleanup", "", "cleanup stored bot data and exit: listings, history, or all")
)

func Run() {
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	printBanner()

	if *generateSearches != "" {
		runGenerator()
		return
	}
	if *cleanup != "" {
		if err := runCleanup(*cleanup); err != nil {
			slog.Error("cleanup failed", "error", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	db, err := store.New("marktbot.db")
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	registry := marketplace.NewRegistry()
	mp := marktplaatsmp.New(cfg.Marktplaats)
	registry.Register(mp)

	rsn := reasoner.New(cfg.AI)
	sc := scorer.New(db, cfg.Scoring, rsn)
	discord := notify.NewDiscord(cfg.Discord.WebhookURL)
	msg := messenger.New(cfg.Messenger, db, *dryRun)
	asst := assistant.New(cfg, db, mp, sc)
	bot, err := discordbot.New(cfg.Discord, asst)
	if err != nil {
		slog.Error("failed to initialize discord assistant", "error", err)
		os.Exit(1)
	}

	if cfg.Messenger.Enabled && !*dryRun {
		if err := msg.Init(); err != nil {
			slog.Error("failed to initialize messenger", "error", err)
			os.Exit(1)
		}
		defer msg.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if bot != nil {
		if err := bot.Start(ctx); err != nil {
			slog.Error("failed to start discord assistant", "error", err)
			os.Exit(1)
		}
		defer bot.Close()
	}

	sched := scheduler.New(cfg, registry, db, sc, discord, msg, *dryRun)
	if len(cfg.Searches) == 0 {
		slog.Info("no static searches configured; running discord assistant only")
		<-ctx.Done()
		return
	}
	if err := sched.Run(ctx, *once); err != nil {
		slog.Error("scheduler error", "error", err)
		os.Exit(1)
	}
}

func runGenerator() {
	genCfg, err := config.LoadForGeneration(*configPath)
	if err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to load config for generator, using defaults", "error", err)
	}
	aiCfg := config.AIConfig{}
	if genCfg != nil {
		aiCfg = genCfg.AI
	}
	gen := generator.New(aiCfg)
	searches, err := gen.GenerateSearches(context.Background(), *generateSearches)
	if err != nil {
		slog.Warn("search generation completed with fallback", "error", err)
	}
	if err := generator.PrintSearches(searches); err != nil {
		slog.Error("failed to print generated searches", "error", err)
		os.Exit(1)
	}
}

func printBanner() {
	fmt.Print(`
  __  __            _    _   ____        _
 |  \/  | __ _ _ __| | _| |_| __ )  ___ | |_
 | |\/| |/ _' | '__| |/ / __|  _ \ / _ \| __|
 | |  | | (_| | |  |   <| |_| |_) | (_) | |_
 |_|  |_|\__,_|_|  |_|\_\\__|____/ \___/ \__|

  Marktplaats Deal Finder & Auto-Negotiator
`)
}

func runCleanup(mode string) error {
	includeListings := false
	includeHistory := false
	switch mode {
	case "listings":
		includeListings = true
	case "history":
		includeHistory = true
	case "all":
		includeListings = true
		includeHistory = true
	default:
		return fmt.Errorf("unsupported cleanup mode %q (use listings, history, or all)", mode)
	}

	db, err := store.New("marktbot.db")
	if err != nil {
		return fmt.Errorf("opening database for cleanup: %w", err)
	}
	defer db.Close()
	_, err = db.Cleanup(includeListings, includeHistory)
	return err
}
