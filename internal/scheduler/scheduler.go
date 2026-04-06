package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/format"
	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/messenger"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

type Scheduler struct {
	cfg       *config.Config
	registry  *marketplace.Registry
	store     store.Store
	scorer    *scorer.Scorer
	discord   *notify.Discord
	messenger *messenger.Messenger
	dryRun    bool
}

func New(
	cfg *config.Config,
	registry *marketplace.Registry,
	s store.Store,
	sc *scorer.Scorer,
	discord *notify.Discord,
	msg *messenger.Messenger,
	dryRun bool,
) *Scheduler {
	return &Scheduler{
		cfg:       cfg,
		registry:  registry,
		store:     s,
		scorer:    sc,
		discord:   discord,
		messenger: msg,
		dryRun:    dryRun,
	}
}

// Run executes the main loop. If once is true, runs a single cycle and returns.
func (s *Scheduler) Run(ctx context.Context, once bool) error {
	for {
		if err := s.runCycle(ctx); err != nil {
			slog.Error("cycle failed", "error", err)
		}

		if once {
			slog.Info("one-shot mode, exiting")
			return nil
		}

		slog.Info("sleeping until next cycle", "interval", s.cfg.Marktplaats.CheckInterval)

		select {
		case <-ctx.Done():
			slog.Info("shutting down scheduler")
			return nil
		case <-time.After(s.cfg.Marktplaats.CheckInterval):
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) error {
	slog.Info("starting search cycle", "searches", len(s.cfg.Searches))

	var messageQueue []messageJob
	for _, searchCfg := range s.cfg.Searches {
		deals, jobs, err := s.processSearch(ctx, searchCfg)
		if err != nil {
			slog.Error("search failed", "name", searchCfg.Name, "error", err)
			continue
		}

		slog.Info("search results", "name", searchCfg.Name, "deals_found", deals)
		messageQueue = append(messageQueue, jobs...)
	}

	if len(messageQueue) > 0 {
		s.processMessages(messageQueue)
	}

	return nil
}

type messageJob struct {
	scored models.ScoredListing
	search models.SearchSpec
}

func (s *Scheduler) processSearch(ctx context.Context, searchCfg config.SearchConfig) (int, []messageJob, error) {
	spec := searchCfg.ToSpec()
	mp, ok := s.registry.Get(spec.MarketplaceID)
	if !ok {
		return 0, nil, fmt.Errorf("marketplace %q is not registered", spec.MarketplaceID)
	}

	listings, err := mp.Search(ctx, spec)
	if err != nil {
		return 0, nil, fmt.Errorf("searching: %w", err)
	}

	var dealCount int
	var jobs []messageJob

	for _, listing := range listings {
		if listing.Price > 0 {
			if err := s.store.RecordPrice(spec.Query, spec.CategoryID, listing.Price); err != nil {
				slog.Warn("failed to record price", "error", err)
			}
		}

		isNew, err := s.store.IsNew("", listing.ItemID)
		if err != nil {
			slog.Warn("failed to check listing", "error", err)
			continue
		}

		previousScore, hadPreviousScore, err := s.store.GetListingScore("", listing.ItemID)
		if err != nil {
			slog.Warn("failed to load previous listing score", "error", err)
			continue
		}

		scored := s.scorer.Score(ctx, listing, spec)

		if err := s.store.SaveListing("", listing, spec.Query, scored.Score); err != nil {
			slog.Warn("failed to save listing", "error", err)
		}

		crossedThreshold := !isNew && hadPreviousScore &&
			previousScore < s.cfg.Scoring.MinScore &&
			scored.Score >= s.cfg.Scoring.MinScore

		if !isNew && !crossedThreshold {
			continue
		}

		if scored.Score < s.cfg.Scoring.MinScore {
			slog.Debug(
				"listing below min score",
				"title", listing.Title,
				"score", scored.Score,
				"min", s.cfg.Scoring.MinScore,
			)
			continue
		}

		if listing.Price <= 0 || scored.OfferPrice <= 0 {
			slog.Debug(
				"listing skipped because it has no actionable price",
				"title", listing.Title,
				"price_type", listing.PriceType,
				"score", scored.Score,
			)
			continue
		}

		dealCount++
		if crossedThreshold {
			slog.Info(
				"listing crossed notification threshold",
				"title", listing.Title,
				"previous_score", fmt.Sprintf("%.1f", previousScore),
				"score", fmt.Sprintf("%.1f", scored.Score),
			)
		}
		slog.Info(
			"deal found",
			"title", listing.Title,
			"price", format.Euro(listing.Price),
			"score", fmt.Sprintf("%.1f", scored.Score),
			"offer", format.Euro(scored.OfferPrice),
			"fair_price", format.Euro(scored.FairPrice),
			"confidence", fmt.Sprintf("%.2f", scored.Confidence),
			"source", scored.ReasoningSource,
			"reason", scored.Reason,
			"url", listing.URL,
		)

		if scored.SearchAdvice != "" {
			slog.Info("search advice", "query", spec.Name, "advice", scored.SearchAdvice)
		}

		if s.discord.Enabled() {
			if err := s.discord.SendDeal(scored, spec.Name); err != nil {
				slog.Warn("failed to send Discord notification", "error", err)
			}
		}

		if s.cfg.AI.Enabled && scored.Confidence < s.cfg.AI.MinConfidence {
			slog.Info(
				"skipping auto-message because deal confidence is below threshold",
				"title", listing.Title,
				"confidence", fmt.Sprintf("%.2f", scored.Confidence),
				"required", fmt.Sprintf("%.2f", s.cfg.AI.MinConfidence),
			)
			continue
		}

		if spec.AutoMessage && s.messenger.Enabled() {
			jobs = append(jobs, messageJob{scored: scored, search: spec})
		}
	}

	return dealCount, jobs, nil
}

func (s *Scheduler) processMessages(jobs []messageJob) {
	for _, job := range jobs {
		if s.dryRun {
			slog.Info(
				"DRY RUN: would message seller",
				"title", job.scored.Listing.Title,
				"offer", format.Euro(job.scored.OfferPrice),
			)
			continue
		}

		if err := s.messenger.SendMessage(job.scored, job.search); err != nil {
			slog.Warn("failed to send message", "item", job.scored.Listing.ItemID, "error", err)
		}

		time.Sleep(5 * time.Second)
	}
}
