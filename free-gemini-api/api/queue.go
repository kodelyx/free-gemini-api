package api

import (
	"context"
	"fmt"
	"goapi/db"
	"goapi/gemini"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// isReservedAccount returns true if accountID is listed in GEMINI_RESERVE_ACCOUNTS (comma-separated).
// Reserved accounts are kept as Pass-2 standby behind frontline workers.
func isReservedAccount(accountID string) bool {
	if accountID == "" {
		return false
	}
	raw := strings.TrimSpace(os.Getenv("GEMINI_RESERVE_ACCOUNTS"))
	if raw == "" {
		return false
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), accountID) {
			return true
		}
	}
	return false
}

// AccountWorker represents an individual Google Account worker in the pool
type AccountWorker struct {
	ID              string               `json:"id"`
	CookiePath      string               `json:"cookie_path"`
	Client          *gemini.GeminiClient `json:"-"`
	Tier            string               `json:"tier"` // "free" or "subscription"
	InFlight        int64                `json:"in_flight"`
	TotalServed     int64                `json:"total_served"`
	TotalErrors     int64                `json:"total_errors"`
	Consecutive429s int64                `json:"consecutive_429s"`
	Status          string               `json:"status"` // "active", "busy", "cooldown", "recovering"
	CooldownUntil   time.Time            `json:"cooldown_until"`
	LastUsed        time.Time            `json:"last_used"`
	mu              sync.Mutex
}

// IsHealthy returns true if the worker is not in cooldown and is ready to accept requests
func (w *AccountWorker) IsHealthy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.CooldownUntil.IsZero() && time.Now().Before(w.CooldownUntil) {
		return false
	}
	if w.Status == "cooldown" || w.Status == "recovering" {
		w.Status = "active"
		w.CooldownUntil = time.Time{}
	}
	return w.Client != nil
}

// WorkerPool manages the pool of Google account workers, load-balancing, and session affinity
type WorkerPool struct {
	mu                      sync.RWMutex
	workers                 []*AccountWorker
	affinityMap             sync.Map // sessionKey / convID / agentID -> AccountID
	rrIndex                 uint64   // atomic round-robin counter
	maxConcurrencyPerWorker int64    // max parallel requests per Google account (default: 3)
}

var (
	GlobalWorkerPool *WorkerPool
	poolOnce         sync.Once
)

// GetWorkerPool returns the singleton WorkerPool instance, initializing if needed
func GetWorkerPool() *WorkerPool {
	poolOnce.Do(func() {
		GlobalWorkerPool = &WorkerPool{
			maxConcurrencyPerWorker: 3,
		}
		GlobalWorkerPool.ReloadPool()
	})
	return GlobalWorkerPool
}

// InitWorkerPool initializes the global worker pool at server startup
func InitWorkerPool() {
	_ = GetWorkerPool()
}

// ReloadPool scans cookies directory, boots up new workers, and reloads active sessions
func (p *WorkerPool) ReloadPool() {
	p.mu.Lock()
	defer p.mu.Unlock()

	accountFiles := gemini.GetAvailableAccountCookieFiles()
	if len(accountFiles) == 0 {
		accountFiles = []string{CookiesFile}
	}

	existingMap := make(map[string]*AccountWorker)
	for _, w := range p.workers {
		existingMap[w.CookiePath] = w
	}

	var updatedWorkers []*AccountWorker

	for _, cFile := range accountFiles {
		accID := filepath.Base(cFile)
		accID = strings.TrimPrefix(accID, "account_")
		accID = strings.TrimSuffix(accID, ".json")
		if gemini.IsIgnoredAccount(accID) {
			continue
		}

		if existing, ok := existingMap[cFile]; ok {
			// Existing worker — reload session
			if existing.Client != nil {
				_ = existing.Client.ReloadSession()
				workerTier := existing.Client.AccountTier
				if workerTier == "" || workerTier == "free" {
					workerTier = "subscription"
				}
				existing.Tier = workerTier
			}
			updatedWorkers = append(updatedWorkers, existing)
			continue
		}

		// Check if file exists before creating client
		if _, err := os.Stat(cFile); os.IsNotExist(err) {
			continue
		}

		client, err := gemini.NewClient(cFile)
		if err != nil {
			log.Printf("⚠️ [WorkerPool] Could not initialize client for %s: %v", cFile, err)
			continue
		}
		_ = client.InitSession()

		workerTier := client.AccountTier
		if workerTier == "" || workerTier == "free" {
			workerTier = "subscription"
		}
		worker := &AccountWorker{
			ID:          accID,
			CookiePath:  cFile,
			Client:      client,
			Tier:        workerTier,
			Status:      "active",
			LastUsed:    time.Now(),
		}
		updatedWorkers = append(updatedWorkers, worker)
		_ = db.RecordAccountUsage(accID, cFile, "active", client.AccountTier)
		log.Printf("🚀 [WorkerPool] Registered new Account Worker [%s] (Tier: %s) from %s", accID, worker.Tier, cFile)
	}

	// Ensure a Guest Worker is available for zero-login free requests or as fallback
	hasGuest := false
	for _, w := range updatedWorkers {
		if w.ID == "guest" {
			hasGuest = true
			break
		}
	}
	if !hasGuest {
		guestClient, err := gemini.NewClient("guest")
		if err == nil {
			_ = guestClient.InitSession()
			guestWorker := &AccountWorker{
				ID:         "guest",
				CookiePath: "guest",
				Client:     guestClient,
				Tier:       "free",
				Status:     "active",
				LastUsed:   time.Now(),
			}
			updatedWorkers = append(updatedWorkers, guestWorker)
			_ = db.RecordAccountUsage("guest", "guest", "active", "free")
			log.Printf("🌐 [WorkerPool] Registered Guest Worker [guest] (Tier: free) — Ready out-of-the-box without login!")
		}
	}

	p.workers = updatedWorkers
	log.Printf("✨ [WorkerPool] Pool reloaded: %d active account worker(s) available", len(p.workers))
}

// BindAffinity binds a conversation ID or agent key to an account ID
func (p *WorkerPool) BindAffinity(key, accountID string) {
	if key != "" && accountID != "" {
		p.affinityMap.Store(key, accountID)
	}
}

// AcquireWorker selects the best available worker (legacy wrapper defaulting to free/any tier)
func (p *WorkerPool) AcquireWorker(ctx context.Context, affinityKey string) (*AccountWorker, error) {
	return p.AcquireWorkerWithTier(ctx, affinityKey, "")
}

// AcquireWorkerWithTier selects the best available worker using Sticky Affinity, Tier Matching, or Least-Busy routing
func (p *WorkerPool) AcquireWorkerWithTier(ctx context.Context, affinityKey string, requiredTier string) (*AccountWorker, error) {
	timeout := 45 * time.Second
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for available worker in pool")
		}

		p.mu.RLock()
		workersCount := len(p.workers)
		if workersCount == 0 {
			p.mu.RUnlock()
			// Attempt reload if empty
			p.ReloadPool()
			time.Sleep(500 * time.Millisecond)
			continue
		}

		isPaidTierReq := requiredTier == "subscription" || requiredTier == "ultra"

		// If subscription or ultra is strictly required, ensure at least one paid/authenticated worker exists in pool
		if isPaidTierReq {
			hasSubWorker := false
			for _, w := range p.workers {
				if w.Tier == "subscription" || w.Tier == "ultra" {
					hasSubWorker = true
					break
				}
			}
			if !hasSubWorker {
				for _, w := range p.workers {
					if w.ID != "guest" && w.Tier != "free" {
						hasSubWorker = true
						break
					}
				}
			}
			if !hasSubWorker {
				p.mu.RUnlock()
				return nil, fmt.Errorf("requested model requires a subscription (Gemini Advanced) account, but no subscription accounts are connected in the pool")
			}
		}

		// Check if ALL eligible workers are currently in cooldown / rate-limited
		hasCandidate := false
		allInCooldown := true
		minCooldownRemaining := 24 * time.Hour

		for _, w := range p.workers {
			if isPaidTierReq && w.Tier != "subscription" && w.Tier != "ultra" && w.ID == "guest" {
				continue
			}
			hasCandidate = true
			w.mu.Lock()
			if w.CooldownUntil.IsZero() || time.Now().After(w.CooldownUntil) {
				allInCooldown = false
			} else {
				rem := time.Until(w.CooldownUntil)
				if rem < minCooldownRemaining {
					minCooldownRemaining = rem
				}
			}
			w.mu.Unlock()
		}

		if hasCandidate && allInCooldown {
			p.mu.RUnlock()
			if minCooldownRemaining <= 3*time.Second {
				time.Sleep(minCooldownRemaining)
				continue
			}
			secs := int(minCooldownRemaining.Seconds()) + 1
			tierName := "free"
			if requiredTier == "ultra" {
				tierName = "ultra"
			} else if requiredTier == "subscription" {
				tierName = "subscription"
			}
			return nil, fmt.Errorf("rate_limit_exceeded: all %s tier Google accounts reached rate limit / quota. Cooldown active, next worker ready in %ds. Connect more accounts to increase quota", tierName, secs)
		}

		hasExplicitUltra := false
		hasExplicitSub := false
		for _, w := range p.workers {
			if w.ID == "guest" {
				continue
			}
			if w.Tier == "ultra" {
				hasExplicitUltra = true
				hasExplicitSub = true
			} else if w.Tier == "subscription" {
				hasExplicitSub = true
			}
		}

		// 1. Check Sticky Affinity (if worker matches required tier)
		if affinityKey != "" {
			if targetID, ok := p.affinityMap.Load(affinityKey); ok {
				for _, w := range p.workers {
					if w.ID == targetID && w.IsHealthy() && atomic.LoadInt64(&w.InFlight) < p.maxConcurrencyPerWorker {
						// Ensure tier compatibility
						tierOK := !isPaidTierReq || w.Tier == "subscription" || w.Tier == "ultra" || w.ID != "guest"
						if requiredTier == "ultra" && hasExplicitUltra && w.Tier != "ultra" {
							tierOK = false
						}
						if tierOK {
							atomic.AddInt64(&w.InFlight, 1)
							w.mu.Lock()
							w.LastUsed = time.Now()
							w.mu.Unlock()
							p.mu.RUnlock()
							return w, nil
						}
					}
				}
			}
		}

		// 2. Least-Busy Load Balancing
		var bestWorker *AccountWorker
		minInFlight := int64(999999)
		offset := atomic.AddUint64(&p.rrIndex, 1) % uint64(workersCount)

		if isPaidTierReq {
			// Pass 1: Prefer frontline non-reserved workers first
			// Pass 2: Fall back to reserved accounts (GEMINI_RESERVE_ACCOUNTS) if all frontline workers are busy or in cooldown
			for _, allowReserved := range []bool{false, true} {
				if bestWorker != nil {
					break
				}
				for i := 0; i < workersCount; i++ {
					idx := (int(offset) + i) % workersCount
					w := p.workers[idx]
					if !w.IsHealthy() || w.ID == "guest" {
						continue
					}
					if !allowReserved && isReservedAccount(w.ID) {
						continue
					}
					if requiredTier == "ultra" && hasExplicitUltra {
						if w.Tier != "ultra" {
							continue
						}
					} else if hasExplicitSub && w.Tier != "subscription" && w.Tier != "ultra" {
						continue
					}
					inFlight := atomic.LoadInt64(&w.InFlight)
					if inFlight < p.maxConcurrencyPerWorker && inFlight < minInFlight {
						minInFlight = inFlight
						bestWorker = w
					}
				}
			}
		} else {
			// Free / General requests:
			// Pass 1: Prefer free authenticated workers (non-reserved, non-guest)
			for i := 0; i < workersCount; i++ {
				idx := (int(offset) + i) % workersCount
				w := p.workers[idx]
				if !w.IsHealthy() || w.Tier != "free" || w.ID == "guest" || isReservedAccount(w.ID) {
					continue
				}
				inFlight := atomic.LoadInt64(&w.InFlight)
				if inFlight < p.maxConcurrencyPerWorker && inFlight < minInFlight {
					minInFlight = inFlight
					bestWorker = w
				}
			}

			// Pass 2: Prefer frontline subscription workers (non-reserved, non-guest)
			if bestWorker == nil {
				for i := 0; i < workersCount; i++ {
					idx := (int(offset) + i) % workersCount
					w := p.workers[idx]
					if !w.IsHealthy() || w.ID == "guest" || isReservedAccount(w.ID) {
						continue
					}
					inFlight := atomic.LoadInt64(&w.InFlight)
					if inFlight < p.maxConcurrencyPerWorker && inFlight < minInFlight {
						minInFlight = inFlight
						bestWorker = w
					}
				}
			}

			// Pass 3: If frontline workers are busy or in cooldown, allow reserved accounts
			if bestWorker == nil {
				for i := 0; i < workersCount; i++ {
					idx := (int(offset) + i) % workersCount
					w := p.workers[idx]
					if !w.IsHealthy() || w.ID == "guest" {
						continue
					}
					inFlight := atomic.LoadInt64(&w.InFlight)
					if inFlight < p.maxConcurrencyPerWorker && inFlight < minInFlight {
						minInFlight = inFlight
						bestWorker = w
					}
				}
			}

			// Pass 4: Fall back to guest worker if no authenticated workers available or all on cooldown
			if bestWorker == nil {
				for i := 0; i < workersCount; i++ {
					idx := (int(offset) + i) % workersCount
					w := p.workers[idx]
					if !w.IsHealthy() {
						continue
					}
					inFlight := atomic.LoadInt64(&w.InFlight)
					if inFlight < p.maxConcurrencyPerWorker && inFlight < minInFlight {
						minInFlight = inFlight
						bestWorker = w
					}
				}
			}
		}

		if bestWorker != nil {
			atomic.AddInt64(&bestWorker.InFlight, 1)
			bestWorker.mu.Lock()
			sinceLast := time.Since(bestWorker.LastUsed)
			bestWorker.LastUsed = time.Now()
			bestWorker.mu.Unlock()

			if affinityKey != "" {
				p.affinityMap.Store(affinityKey, bestWorker.ID)
			}
			p.mu.RUnlock()

			// Human Micro-Jitter: if the same account is hit consecutively within < 1s, pause 150-350ms
			if sinceLast < time.Second {
				jitterMs := 150 + rand.Intn(201)
				time.Sleep(time.Duration(jitterMs) * time.Millisecond)
			}
			return bestWorker, nil
		}

		p.mu.RUnlock()
		// All workers at max concurrency or in cooldown — wait briefly
		time.Sleep(100 * time.Millisecond)
	}
}

// ReleaseWorker releases a worker, updates metrics, and triggers cooldown on 429/auth errors
func (p *WorkerPool) ReleaseWorker(worker *AccountWorker, err error) {
	if worker == nil {
		return
	}

	atomic.AddInt64(&worker.InFlight, -1)

	worker.mu.Lock()
	defer worker.mu.Unlock()

	if err == nil {
		atomic.AddInt64(&worker.TotalServed, 1)
		worker.Consecutive429s = 0
		worker.Status = "active"
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "active", worker.Tier)
		return
	}

	atomic.AddInt64(&worker.TotalErrors, 1)
	errStr := strings.ToLower(err.Error())

	isRateLimit := strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "quota") ||
		strings.Contains(errStr, "resource_exhausted")

	isAuthOrExpiry := strings.Contains(errStr, "expired") ||
		strings.Contains(errStr, "servicelogin") ||
		strings.Contains(errStr, "snlm0e") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "unauthorized")

	if isRateLimit {
		worker.Consecutive429s++
		cooldownDur := 60 * time.Second
		if worker.Consecutive429s == 2 {
			cooldownDur = 180 * time.Second
		} else if worker.Consecutive429s >= 3 {
			cooldownDur = 600 * time.Second
		}
		worker.Status = "cooldown"
		worker.CooldownUntil = time.Now().Add(cooldownDur)
		log.Printf("⏳ [WorkerPool] Worker [%s] (Tier: %s) rate limited (429/quota, streak=%d). In cooldown for %v.", worker.ID, worker.Tier, worker.Consecutive429s, cooldownDur)
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "rate_limited", worker.Tier)
	} else if isAuthOrExpiry {
		worker.Status = "recovering"
		worker.CooldownUntil = time.Now().Add(15 * time.Second)
		log.Printf("🚨 [WorkerPool] Worker [%s] auth/session issue detected. Triggering cookie sync...", worker.ID)
		gemini.BroadcastCookieRefresh()
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "error", worker.Tier)
	} else {
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "error", worker.Tier)
	}
}

// ExecuteQueued dispatches a request through the worker pool with automatic retry on alternative workers
func (p *WorkerPool) ExecuteQueued(ctx context.Context, affinityKey string, op func(*gemini.GeminiClient) (*gemini.GeminiResponse, error)) (*gemini.GeminiResponse, error) {
	return p.ExecuteQueuedWithTier(ctx, affinityKey, "", func(w *AccountWorker) (*gemini.GeminiResponse, error) {
		return op(w.Client)
	})
}

// ExecuteQueuedWithTier handles request execution with specific tier requirement
func (p *WorkerPool) ExecuteQueuedWithTier(ctx context.Context, affinityKey string, requiredTier string, op func(w *AccountWorker) (*gemini.GeminiResponse, error)) (*gemini.GeminiResponse, error) {
	maxAttempts := 5
	p.mu.RLock()
	availableCount := len(p.workers)
	p.mu.RUnlock()

	if availableCount > 0 && availableCount < maxAttempts {
		maxAttempts = availableCount
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		worker, err := p.AcquireWorkerWithTier(ctx, affinityKey, requiredTier)
		if err != nil {
			return nil, fmt.Errorf("failed to acquire worker from pool: %w", err)
		}

		resp, opErr := op(worker)
		p.ReleaseWorker(worker, opErr)

		if opErr == nil {
			if resp != nil && resp.ConversationID != "" {
				p.BindAffinity(resp.ConversationID, worker.ID)
				if affinityKey != "" {
					p.BindAffinity(affinityKey, worker.ID)
				}
			}
			return resp, nil
		}

		lastErr = opErr
		log.Printf("⚠️ [WorkerPool] Worker [%s] (Tier: %s) attempt %d/%d failed: %v. Retrying on another worker...", worker.ID, worker.Tier, attempt, maxAttempts, opErr)

		// On failure, remove affinity to avoid sticking to a broken worker
		if affinityKey != "" {
			p.affinityMap.Delete(affinityKey)
		}

		time.Sleep(200 * time.Millisecond)
	}

	return nil, lastErr
}

// ExecuteQueuedStream handles streaming completions through the worker queue with guaranteed release
func (p *WorkerPool) ExecuteQueuedStream(ctx context.Context, affinityKey string, op func(*gemini.GeminiClient) error) error {
	return p.ExecuteQueuedStreamWithTier(ctx, affinityKey, "", func(w *AccountWorker) error {
		return op(w.Client)
	})
}

// ExecuteQueuedStreamWithTier handles streaming completions requiring a specific account tier
func (p *WorkerPool) ExecuteQueuedStreamWithTier(ctx context.Context, affinityKey string, requiredTier string, op func(w *AccountWorker) error) error {
	worker, err := p.AcquireWorkerWithTier(ctx, affinityKey, requiredTier)
	if err != nil {
		return fmt.Errorf("failed to acquire worker for streaming: %w", err)
	}
	var streamErr error
	defer func() {
		p.ReleaseWorker(worker, streamErr)
	}()

	streamErr = op(worker)
	if streamErr != nil {
		return streamErr
	}

	if worker.Client != nil && worker.Client.ConversationID != "" {
		p.BindAffinity(worker.Client.ConversationID, worker.ID)
		if affinityKey != "" {
			p.BindAffinity(affinityKey, worker.ID)
		}
	}
	return nil
}

// WorkerPoolStats represents the live metrics of the worker pool
type WorkerPoolStats struct {
	TotalWorkers        int           `json:"total_workers"`
	ActiveWorkers       int           `json:"active_workers"`
	FreeWorkers         int           `json:"free_workers"`
	SubscriptionWorkers int           `json:"subscription_workers"`
	TotalInFlight       int64         `json:"total_in_flight"`
	MaxConcurrency      int64         `json:"max_concurrency_per_worker"`
	Workers             []WorkerStats `json:"workers"`
}

type WorkerStats struct {
	ID                    string `json:"id"`
	CookiePath            string `json:"cookie_path"`
	Status                string `json:"status"`
	Tier                  string `json:"tier"`
	InFlight              int64  `json:"in_flight"`
	TotalServed           int64  `json:"total_served"`
	TotalErrors           int64  `json:"total_errors"`
	CooldownSecsRemaining int    `json:"cooldown_seconds_remaining"`
	LastUsed              string `json:"last_used"`
}

// GetStats returns real-time snapshot of the worker pool
func (p *WorkerPool) GetStats() WorkerPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := WorkerPoolStats{
		TotalWorkers:   len(p.workers),
		MaxConcurrency: p.maxConcurrencyPerWorker,
		Workers:        make([]WorkerStats, 0, len(p.workers)),
	}

	for _, w := range p.workers {
		inFlight := atomic.LoadInt64(&w.InFlight)
		stats.TotalInFlight += inFlight

		w.mu.Lock()
		cooldownRemaining := 0
		if !w.CooldownUntil.IsZero() && time.Now().Before(w.CooldownUntil) {
			cooldownRemaining = int(time.Until(w.CooldownUntil).Seconds())
		}
		isHealthy := w.Status == "active" && cooldownRemaining == 0
		if isHealthy {
			stats.ActiveWorkers++
		}

		if w.Tier == "subscription" {
			stats.SubscriptionWorkers++
		} else {
			stats.FreeWorkers++
		}

		ws := WorkerStats{
			ID:                    w.ID,
			CookiePath:            w.CookiePath,
			Status:                w.Status,
			Tier:                  w.Tier,
			InFlight:              inFlight,
			TotalServed:           atomic.LoadInt64(&w.TotalServed),
			TotalErrors:           atomic.LoadInt64(&w.TotalErrors),
			CooldownSecsRemaining: cooldownRemaining,
			LastUsed:              w.LastUsed.Format(time.RFC3339),
		}
		w.mu.Unlock()
		stats.Workers = append(stats.Workers, ws)
	}

	return stats
}

// DiscoverModelsFromWorkers triggers autonomous model and tier discovery on all connected accounts
func (p *WorkerPool) DiscoverModelsFromWorkers() (map[string]any, error) {
	p.mu.RLock()
	workersList := make([]*AccountWorker, len(p.workers))
	copy(workersList, p.workers)
	p.mu.RUnlock()

	discoveredCount := 0
	workerResults := make([]map[string]any, 0)

	for _, w := range workersList {
		if w.Client == nil {
			continue
		}
		newModels, err := gemini.DiscoverAccountModels(w.Client)
		if err == nil {
			discoveredCount += len(newModels)
			w.mu.Lock()
			w.Tier = w.Client.AccountTier
			w.mu.Unlock()
			_ = db.RecordAccountUsage(w.ID, w.CookiePath, w.Status, w.Client.AccountTier)
		}

		res := map[string]any{
			"worker_id":         w.ID,
			"tier":              w.Tier,
			"discovered_models": len(newModels),
		}
		if err != nil {
			res["error"] = err.Error()
		}
		workerResults = append(workerResults, res)
	}

	return map[string]any{
		"workers_checked":  len(workersList),
		"models_found":    discoveredCount,
		"workers":          workerResults,
		"active_registry": gemini.GetModelRegistry().GetAll(),
	}, nil
}
