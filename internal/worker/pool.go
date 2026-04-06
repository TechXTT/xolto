package worker

import (
	"context"
	"sync"
	"time"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

type Pool struct {
	db         store.Store
	registry   *marketplace.Registry
	scorer     *scorer.Scorer
	notifier   notify.Dispatcher
	minScore   float64
	interval   time.Duration
	mu         sync.Mutex
	runningCtx context.Context
	cancel     context.CancelFunc
}

func NewPool(db store.Store, registry *marketplace.Registry, sc *scorer.Scorer, notifier notify.Dispatcher, minScore float64, interval time.Duration) *Pool {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Pool{
		db:       db,
		registry: registry,
		scorer:   sc,
		notifier: notifier,
		minScore: minScore,
		interval: interval,
	}
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

func (p *Pool) RunAllNow(ctx context.Context) error {
	specs, err := p.db.GetAllEnabledSearchConfigs()
	if err != nil {
		return err
	}
	w := &UserWorker{
		specs:    specs,
		db:       p.db,
		registry: p.registry,
		scorer:   p.scorer,
		notifier: p.notifier,
		minScore: p.minScore,
	}
	return w.RunCycle(ctx)
}

func (p *Pool) RunUserNow(ctx context.Context, userID string) error {
	specs, err := p.db.GetSearchConfigs(userID)
	if err != nil {
		return err
	}
	w := &UserWorker{
		specs:    specs,
		db:       p.db,
		registry: p.registry,
		scorer:   p.scorer,
		notifier: p.notifier,
		minScore: p.minScore,
	}
	return w.RunCycle(ctx)
}

func (p *Pool) loop() {
	ticker := time.NewTicker(p.interval)
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
