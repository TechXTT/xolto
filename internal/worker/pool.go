package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

const minPollInterval = 30 * time.Second

type Pool struct {
	db         store.Store
	registry   *marketplace.Registry
	scorer     *scorer.Scorer
	notifier   notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore   float64
	mu         sync.Mutex
	lastRun    map[int64]time.Time // keyed by SearchSpec.ID
	runningCtx context.Context
	cancel     context.CancelFunc
}

func NewPool(db store.Store, registry *marketplace.Registry, sc *scorer.Scorer, notifier notify.Dispatcher, minScore float64, _ time.Duration) *Pool {
	return &Pool{
		db:            db,
		registry:      registry,
		scorer:        sc,
		notifier:      notifier,
		emailNotifier: nil,
		minScore:      minScore,
		lastRun:       make(map[int64]time.Time),
	}
}

// SetEmailNotifier configures an optional email notifier for deal alerts.
func (p *Pool) SetEmailNotifier(e *notify.EmailNotifier) {
	p.emailNotifier = e
}

func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.runningCtx, p.cancel = context.WithCancel(ctx)
	p.mu.Unlock()

	go p.loop()
}

func (p *Pool) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// RunAllNow runs all enabled search specs that are due based on their CheckInterval.
func (p *Pool) RunAllNow(ctx context.Context) error {
	specs, err := p.db.GetAllEnabledSearchConfigs()
	if err != nil {
		return err
	}
	due := p.filterDue(specs)
	mpCounts := map[string]int{}
	for _, s := range due {
		mpCounts[s.MarketplaceID]++
	}
	if len(due) > 0 {
		slog.Info("worker pool tick", "total_specs", len(specs), "due", len(due), "marketplaces", mpCounts)
	}
	if len(due) == 0 {
		return nil
	}
	w := &UserWorker{
		specs:         due,
		db:            p.db,
		registry:      p.registry,
		scorer:        p.scorer,
		notifier:      p.notifier,
		emailNotifier: p.emailNotifier,
		minScore:      p.minScore,
	}
	err = w.RunCycle(ctx)
	p.recordRun(due)
	return err
}

// RunUserNow runs all enabled searches for a specific user immediately,
// ignoring their CheckInterval (explicit user-triggered run).
func (p *Pool) RunUserNow(ctx context.Context, userID string) error {
	specs, err := p.db.GetSearchConfigs(userID)
	if err != nil {
		return err
	}
	enabled := make([]models.SearchSpec, 0, len(specs))
	for _, s := range specs {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	w := &UserWorker{
		specs:         enabled,
		db:            p.db,
		registry:      p.registry,
		scorer:        p.scorer,
		notifier:      p.notifier,
		emailNotifier: p.emailNotifier,
		minScore:      p.minScore,
	}
	err = w.RunCycle(ctx)
	p.recordRun(enabled)
	return err
}

// filterDue returns specs whose CheckInterval has elapsed since last run.
func (p *Pool) filterDue(specs []models.SearchSpec) []models.SearchSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	due := specs[:0:0]
	for _, s := range specs {
		interval := s.CheckInterval
		if interval < minPollInterval {
			interval = minPollInterval
		}
		last, seen := p.lastRun[s.ID]
		if !seen || now.Sub(last) >= interval {
			due = append(due, s)
		}
	}
	return due
}

func (p *Pool) recordRun(specs []models.SearchSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, s := range specs {
		p.lastRun[s.ID] = now
	}
}

func (p *Pool) loop() {
	ticker := time.NewTicker(minPollInterval)
	defer ticker.Stop()

	_ = p.RunAllNow(p.runningCtx)
	for {
		select {
		case <-p.runningCtx.Done():
			return
		case <-ticker.C:
			_ = p.RunAllNow(p.runningCtx)
		}
	}
}
