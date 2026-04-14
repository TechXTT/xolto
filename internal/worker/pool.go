package worker

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/notify"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

const (
	minPollInterval  = 30 * time.Second
	priorityBandSize = 10
)

type Pool struct {
	db            store.Store
	registry      *marketplace.Registry
	scorer        *scorer.Scorer
	notifier      notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore      float64
	tickInterval  time.Duration

	mu              sync.Mutex
	runningCtx      context.Context
	cancel          context.CancelFunc
	overloadedTicks int
}

type candidate struct {
	spec            models.SearchSpec
	user            *models.User
	mission         *models.Mission
	priority        int
	searchesAvoided int
}

type cachedLookups struct {
	users    map[string]*models.User
	missions map[int64]*models.Mission
}

func NewPool(db store.Store, registry *marketplace.Registry, sc *scorer.Scorer, notifier notify.Dispatcher, minScore float64, pollInterval time.Duration) *Pool {
	if pollInterval < minPollInterval {
		pollInterval = minPollInterval
	}
	return &Pool{
		db:            db,
		registry:      registry,
		scorer:        sc,
		notifier:      notifier,
		emailNotifier: nil,
		minScore:      minScore,
		tickInterval:  pollInterval,
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

// RunAllNow runs all enabled search specs that are due according to persisted runtime state.
func (p *Pool) RunAllNow(ctx context.Context) error {
	specs, err := p.db.GetAllEnabledSearchConfigs()
	if err != nil {
		return err
	}
	return p.run(ctx, specs, false)
}

// RunUserNow runs all enabled searches for a specific user immediately,
// ignoring next_run_at while still respecting marketplace concurrency limits.
func (p *Pool) RunUserNow(ctx context.Context, userID string) error {
	specs, err := p.db.GetSearchConfigs(userID)
	if err != nil {
		return err
	}
	enabled := make([]models.SearchSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Enabled {
			enabled = append(enabled, spec)
		}
	}
	return p.run(ctx, enabled, true)
}

func (p *Pool) run(ctx context.Context, specs []models.SearchSpec, force bool) error {
	now := time.Now().UTC()
	candidates, err := p.prepareCandidates(ctx, specs, force, now)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	dispatchBudget := p.dispatchBudget(candidates, force)
	p.updateOverloadState(force, len(candidates) > dispatchBudget && dispatchBudget > 0)
	if !force && p.isOverloaded() {
		candidates = p.deferOverloadedCandidates(candidates, dispatchBudget, now)
	}

	ordered := p.finalizeDispatchOrder(candidates, force)
	if len(ordered) == 0 {
		return nil
	}

	mpCounts := map[string]int{}
	for _, cand := range ordered {
		mpCounts[cand.spec.MarketplaceID]++
	}
	slog.Info("worker pool tick",
		"total_specs", len(specs),
		"dispatching", len(ordered),
		"force", force,
		"marketplaces", mpCounts,
	)
	return p.dispatch(ctx, ordered)
}

func (p *Pool) prepareCandidates(ctx context.Context, specs []models.SearchSpec, force bool, now time.Time) ([]candidate, error) {
	lookups := cachedLookups{
		users:    map[string]*models.User{},
		missions: map[int64]*models.Mission{},
	}

	candidates := make([]candidate, 0, len(specs))
	for _, raw := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !raw.Enabled {
			continue
		}

		user, err := p.cachedUser(&lookups, raw.UserID)
		if err != nil {
			slog.Warn("failed to load user for search", "search_id", raw.ID, "user_id", raw.UserID, "error", err)
			continue
		}
		if user == nil {
			continue
		}
		if !force && !isDue(raw, now) {
			continue
		}

		mission, err := p.cachedMission(&lookups, raw.ProfileID)
		if err != nil {
			slog.Warn("failed to load mission for search", "search_id", raw.ID, "mission_id", raw.ProfileID, "error", err)
			continue
		}
		if mission != nil && (mission.Status == "paused" || mission.Status == "completed") {
			continue
		}

		spec := populateSearchLocation(raw, user, mission)
		if _, ok := p.registry.Get(spec.MarketplaceID); !ok {
			slog.Warn("unknown marketplace for search", "search_id", spec.ID, "marketplace", spec.MarketplaceID)
			continue
		}
		if mission != nil {
			scope, err := missionScopeForCandidate(user, mission)
			if err != nil {
				slog.Warn("failed to resolve mission scope", "search_id", spec.ID, "mission_id", mission.ID, "error", err)
				continue
			}
			if len(scope) > 0 && !scopeContains(scope, spec.MarketplaceID) {
				continue
			}
		}

		priority := computePriority(spec, user, mission, now)
		candidates = append(candidates, candidate{
			spec:            spec,
			user:            user,
			mission:         mission,
			priority:        priority,
			searchesAvoided: searchesAvoidedForCandidate(user, mission),
		})
	}
	return candidates, nil
}

func (p *Pool) finalizeDispatchOrder(candidates []candidate, force bool) []candidate {
	if len(candidates) == 0 {
		return nil
	}
	ordered := roundRobinByPriorityBand(candidates)
	out := make([]candidate, 0, len(ordered))
	dispatchedByUser := map[string]int{}
	dispatchedByMarketplace := map[string]int{}

	for _, cand := range ordered {
		limits := billing.LimitsFor(cand.user.Tier)
		if !force && limits.MaxDispatchPerTick > 0 && dispatchedByUser[cand.user.ID] >= limits.MaxDispatchPerTick {
			continue
		}
		cand.priority -= 10 * dispatchedByMarketplace[cand.spec.MarketplaceID]
		cand.spec.PriorityClass = cand.priority
		dispatchedByUser[cand.user.ID]++
		dispatchedByMarketplace[cand.spec.MarketplaceID]++
		out = append(out, cand)
	}
	return out
}

func roundRobinByPriorityBand(candidates []candidate) []candidate {
	ordered := append([]candidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if priorityBand(ordered[i].priority) != priorityBand(ordered[j].priority) {
			return priorityBand(ordered[i].priority) > priorityBand(ordered[j].priority)
		}
		if ordered[i].priority != ordered[j].priority {
			return ordered[i].priority > ordered[j].priority
		}
		return ordered[i].spec.ID < ordered[j].spec.ID
	})

	out := make([]candidate, 0, len(ordered))
	for start := 0; start < len(ordered); {
		band := priorityBand(ordered[start].priority)
		end := start
		for end < len(ordered) && priorityBand(ordered[end].priority) == band {
			end++
		}
		out = append(out, roundRobinBand(ordered[start:end])...)
		start = end
	}
	return out
}

func roundRobinBand(candidates []candidate) []candidate {
	queues := map[string][]candidate{}
	userOrder := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		if _, ok := queues[cand.user.ID]; !ok {
			userOrder = append(userOrder, cand.user.ID)
		}
		queues[cand.user.ID] = append(queues[cand.user.ID], cand)
	}

	out := make([]candidate, 0, len(candidates))
	for {
		progressed := false
		for _, userID := range userOrder {
			queue := queues[userID]
			if len(queue) == 0 {
				continue
			}
			out = append(out, queue[0])
			queues[userID] = queue[1:]
			progressed = true
		}
		if !progressed {
			return out
		}
	}
}

func (p *Pool) dispatch(ctx context.Context, candidates []candidate) error {
	globalSem := make(chan struct{}, globalInflightLimit())
	userSems := map[string]chan struct{}{}
	marketplaceSems := map[string]chan struct{}{}
	var semMu sync.Mutex

	userWorker := &UserWorker{
		db:            p.db,
		registry:      p.registry,
		scorer:        p.scorer,
		notifier:      p.notifier,
		emailNotifier: p.emailNotifier,
		minScore:      p.minScore,
	}

	getUserSem := func(user *models.User) chan struct{} {
		semMu.Lock()
		defer semMu.Unlock()
		if sem, ok := userSems[user.ID]; ok {
			return sem
		}
		capacity := billing.LimitsFor(user.Tier).MaxConcurrentSearches
		if capacity <= 0 {
			capacity = 1
		}
		sem := make(chan struct{}, capacity)
		userSems[user.ID] = sem
		return sem
	}
	getMarketplaceSem := func(marketplaceID string) chan struct{} {
		semMu.Lock()
		defer semMu.Unlock()
		id := marketplace.NormalizeMarketplaceID(marketplaceID)
		if sem, ok := marketplaceSems[id]; ok {
			return sem
		}
		sem := make(chan struct{}, marketplaceInflightLimit(id))
		marketplaceSems[id] = sem
		return sem
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(candidates))

	for _, cand := range candidates {
		wg.Add(1)
		go func(cand candidate) {
			defer wg.Done()
			enqueuedAt := time.Now().UTC()
			if err := acquire(ctx, globalSem); err != nil {
				errCh <- err
				return
			}
			defer release(globalSem)
			if err := acquire(ctx, getUserSem(cand.user)); err != nil {
				errCh <- err
				return
			}
			defer release(getUserSem(cand.user))
			if err := acquire(ctx, getMarketplaceSem(cand.spec.MarketplaceID)); err != nil {
				errCh <- err
				return
			}
			defer release(getMarketplaceSem(cand.spec.MarketplaceID))

			if err := userWorker.RunTask(ctx, cand, time.Since(enqueuedAt)); err != nil {
				errCh <- err
			}
		}(cand)
	}

	wg.Wait()
	close(errCh)

	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func acquire(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(sem chan struct{}) {
	select {
	case <-sem:
	default:
	}
}

func globalInflightLimit() int {
	return marketplaceInflightLimit("marktplaats") +
		marketplaceInflightLimit("olxbg") +
		marketplaceInflightLimit("vinted_nl") +
		marketplaceInflightLimit("vinted_dk")
}

func marketplaceInflightLimit(marketplaceID string) int {
	switch marketplace.NormalizeMarketplaceID(marketplaceID) {
	case "marktplaats", "olxbg", "vinted_nl", "vinted_dk":
		return 2
	default:
		return 1
	}
}

func computePriority(spec models.SearchSpec, user *models.User, mission *models.Mission, now time.Time) int {
	limits := billing.LimitsFor(user.Tier)
	priority := limits.PlanPriorityWeight
	priority += urgencyWeight(mission)
	priority += stalenessWeight(spec, user, now)
	priority += signalBoost(spec, now)
	priority -= emptyPenalty(spec)
	priority -= failurePenalty(spec)
	return priority
}

func urgencyWeight(mission *models.Mission) int {
	if mission == nil {
		return 0
	}
	switch mission.Urgency {
	case "urgent":
		return 30
	case "flexible":
		return 15
	default:
		return 0
	}
}

func stalenessWeight(spec models.SearchSpec, user *models.User, now time.Time) int {
	overdue := baseIntervalForSpec(spec, user)
	if !spec.NextRunAt.IsZero() {
		if spec.NextRunAt.After(now) {
			return 0
		}
		overdue = now.Sub(spec.NextRunAt)
	}
	overdueMinutes := int(overdue / time.Minute)
	if overdueMinutes <= 0 {
		return 0
	}
	weight := overdueMinutes / 2
	if weight > 40 {
		return 40
	}
	return weight
}

func signalBoost(spec models.SearchSpec, now time.Time) int {
	if spec.LastSignalAt.IsZero() {
		return 0
	}
	age := now.Sub(spec.LastSignalAt)
	switch {
	case age <= 72*time.Hour:
		return 20
	case age <= 7*24*time.Hour:
		return 10
	default:
		return 0
	}
}

func emptyPenalty(spec models.SearchSpec) int {
	penalty := 5 * spec.ConsecutiveEmptyRuns
	if penalty > 25 {
		return 25
	}
	return penalty
}

func failurePenalty(spec models.SearchSpec) int {
	penalty := 15 * spec.ConsecutiveFailures
	if penalty > 60 {
		return 60
	}
	return penalty
}

func isDue(spec models.SearchSpec, now time.Time) bool {
	return spec.NextRunAt.IsZero() || !spec.NextRunAt.After(now)
}

func baseIntervalForSpec(spec models.SearchSpec, user *models.User) time.Duration {
	interval := spec.CheckInterval
	minInterval := minPollInterval
	if user != nil {
		limits := billing.LimitsFor(user.Tier)
		if limits.MinCheckIntervalMins > 0 {
			planMin := time.Duration(limits.MinCheckIntervalMins) * time.Minute
			if planMin > minInterval {
				minInterval = planMin
			}
		}
	}
	if interval < minInterval {
		interval = minInterval
	}
	return interval
}

func nextRunAtAfter(spec models.SearchSpec, user *models.User, now time.Time) time.Time {
	base := baseIntervalForSpec(spec, user)
	return now.Add(base * time.Duration(emptyBackoffMultiplier(spec.ConsecutiveEmptyRuns)) * time.Duration(failureBackoffMultiplier(spec.ConsecutiveFailures)))
}

func emptyBackoffMultiplier(emptyRuns int) int {
	if emptyRuns <= 0 {
		return 1
	}
	multiplier := 1 << minInt(emptyRuns, 3)
	if multiplier > 8 {
		return 8
	}
	return multiplier
}

func failureBackoffMultiplier(failures int) int {
	if failures <= 0 {
		return 1
	}
	multiplier := 1 << minInt(failures, 4)
	if multiplier > 16 {
		return 16
	}
	return multiplier
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func priorityBand(priority int) int {
	return priority / priorityBandSize
}

func populateSearchLocation(spec models.SearchSpec, user *models.User, mission *models.Mission) models.SearchSpec {
	spec.MarketplaceID = marketplace.NormalizeMarketplaceID(spec.MarketplaceID)
	if spec.CountryCode == "" {
		switch {
		case mission != nil && mission.CountryCode != "":
			spec.CountryCode = mission.CountryCode
		case user != nil:
			spec.CountryCode = user.CountryCode
		}
	}
	if spec.City == "" {
		switch {
		case mission != nil && mission.City != "":
			spec.City = mission.City
		case user != nil:
			spec.City = user.City
		}
	}
	if spec.PostalCode == "" {
		switch {
		case mission != nil && mission.PostalCode != "":
			spec.PostalCode = mission.PostalCode
		case mission != nil && mission.ZipCode != "":
			spec.PostalCode = mission.ZipCode
		case user != nil:
			spec.PostalCode = user.PostalCode
		}
	}
	if spec.RadiusKm <= 0 {
		switch {
		case mission != nil && mission.TravelRadius > 0:
			spec.RadiusKm = mission.TravelRadius
		case mission != nil && mission.Distance > 0:
			spec.RadiusKm = mission.Distance / 1000
		case user != nil && user.PreferredRadiusKm > 0:
			spec.RadiusKm = user.PreferredRadiusKm
		}
	}
	return spec
}

func missionScopeForCandidate(user *models.User, mission *models.Mission) ([]string, error) {
	if mission == nil {
		return nil, nil
	}
	countryCode := mission.CountryCode
	if countryCode == "" && user != nil {
		countryCode = user.CountryCode
	}
	crossBorder := mission.CrossBorderEnabled
	if user != nil && mission.CountryCode == "" && !mission.CrossBorderEnabled {
		crossBorder = user.CrossBorderEnabled
	}
	return marketplace.ValidateScope(countryCode, crossBorder, mission.MarketplaceScope), nil
}

func scopeContains(scope []string, marketplaceID string) bool {
	normalized := marketplace.NormalizeMarketplaceID(marketplaceID)
	for _, candidate := range scope {
		if marketplace.NormalizeMarketplaceID(candidate) == normalized {
			return true
		}
	}
	return false
}

func searchesAvoidedForCandidate(user *models.User, mission *models.Mission) int {
	if mission == nil {
		return 0
	}
	countryCode := mission.CountryCode
	if countryCode == "" && user != nil {
		countryCode = user.CountryCode
	}
	if countryCode == "" {
		return 0
	}
	candidateCount := len(marketplace.ScopeCandidates(countryCode, mission.CrossBorderEnabled))
	scope, err := missionScopeForCandidate(user, mission)
	if err != nil {
		return 0
	}
	if candidateCount <= len(scope) {
		return 0
	}
	return candidateCount - len(scope)
}

func (p *Pool) dispatchBudget(candidates []candidate, force bool) int {
	if force {
		return len(candidates)
	}
	budget := 0
	seenUsers := map[string]bool{}
	for _, cand := range candidates {
		if seenUsers[cand.user.ID] {
			continue
		}
		seenUsers[cand.user.ID] = true
		limit := billing.LimitsFor(cand.user.Tier).MaxDispatchPerTick
		if limit <= 0 {
			limit = 1
		}
		budget += limit
	}
	return budget
}

func (p *Pool) deferOverloadedCandidates(candidates []candidate, dispatchBudget int, now time.Time) []candidate {
	if dispatchBudget <= 0 || len(candidates) <= dispatchBudget {
		return candidates
	}
	working := append([]candidate(nil), candidates...)
	sort.SliceStable(working, func(i, j int) bool {
		if working[i].priority != working[j].priority {
			return working[i].priority < working[j].priority
		}
		return working[i].spec.ID < working[j].spec.ID
	})

	keep := map[int64]bool{}
	for _, cand := range working {
		keep[cand.spec.ID] = true
	}
	remaining := len(working)
	for _, cand := range working {
		if remaining <= dispatchBudget {
			break
		}
		if cand.spec.ConsecutiveEmptyRuns <= 0 {
			continue
		}
		if billing.NormalizeTier(cand.user.Tier) != "free" && cand.priority > 15 {
			continue
		}
		keep[cand.spec.ID] = false
		remaining--
		deferred := cand.spec
		deferred.PriorityClass = cand.priority
		deferred.NextRunAt = now.Add(baseIntervalForSpec(deferred, cand.user))
		if err := p.db.UpdateSearchRuntime(deferred); err != nil {
			slog.Warn("failed to defer overloaded search", "search_id", deferred.ID, "error", err)
		}
		if err := p.db.RecordSearchRun(models.SearchRunLog{
			SearchConfigID:  deferred.ID,
			UserID:          deferred.UserID,
			MissionID:       deferred.ProfileID,
			Plan:            billing.NormalizeTier(cand.user.Tier),
			MarketplaceID:   deferred.MarketplaceID,
			CountryCode:     deferred.CountryCode,
			StartedAt:       now,
			FinishedAt:      now,
			Priority:        cand.priority,
			Status:          "deferred_overload",
			Throttled:       true,
			SearchesAvoided: cand.searchesAvoided,
		}); err != nil {
			slog.Warn("failed to log deferred overloaded search", "search_id", deferred.ID, "error", err)
		}
	}

	out := make([]candidate, 0, remaining)
	for _, cand := range candidates {
		if keep[cand.spec.ID] {
			out = append(out, cand)
		}
	}
	return out
}

func (p *Pool) updateOverloadState(force bool, overloaded bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if force {
		return
	}
	if overloaded {
		p.overloadedTicks++
		return
	}
	p.overloadedTicks = 0
}

func (p *Pool) isOverloaded() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.overloadedTicks >= 2
}

func (p *Pool) cachedUser(lookups *cachedLookups, userID string) (*models.User, error) {
	if userID == "" {
		return nil, nil
	}
	if user, ok := lookups.users[userID]; ok {
		return user, nil
	}
	user, err := p.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	lookups.users[userID] = user
	return user, nil
}

func (p *Pool) cachedMission(lookups *cachedLookups, missionID int64) (*models.Mission, error) {
	if missionID <= 0 {
		return nil, nil
	}
	if mission, ok := lookups.missions[missionID]; ok {
		return mission, nil
	}
	mission, err := p.db.GetMission(missionID)
	if err != nil {
		return nil, err
	}
	lookups.missions[missionID] = mission
	return mission, nil
}

func (p *Pool) loop() {
	ticker := time.NewTicker(p.tickInterval)
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
