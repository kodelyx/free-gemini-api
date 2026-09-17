package api

import (
	"context"
	"fmt"
	"goapi/db"
	"goapi/gemini"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AccountWorker represents an individual Google Account worker in the pool
type AccountWorker struct {
	ID            string               `json:"id"`
	CookiePath    string               `json:"cookie_path"`
	Client        *gemini.GeminiClient `json:"-"`
	InFlight      int64                `json:"in_flight"`
	TotalServed   int64                `json:"total_served"`
	TotalErrors   int64                `json:"total_errors"`
	Status        string               `json:"status"` // "active", "busy", "cooldown", "recovering"
	CooldownUntil time.Time            `json:"cooldown_until"`
	LastUsed      time.Time            `json:"last_used"`
	mu            sync.Mutex
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

		if existing, ok := existingMap[cFile]; ok {
			// Existing worker — reload session
			if existing.Client != nil {
				_ = existing.Client.ReloadSession()
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

		worker := &AccountWorker{
			ID:          accID,
			CookiePath:  cFile,
			Client:      client,
			Status:      "active",
			LastUsed:    time.Now(),
		}
		updatedWorkers = append(updatedWorkers, worker)
		log.Printf("🚀 [WorkerPool] Registered new Account Worker [%s] from %s", accID, cFile)
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

// AcquireWorker selects the best available worker using Sticky Affinity or Least-Busy routing
func (p *WorkerPool) AcquireWorker(ctx context.Context, affinityKey string) (*AccountWorker, error) {
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

		// 1. Check Sticky Affinity
		if affinityKey != "" {
			if targetID, ok := p.affinityMap.Load(affinityKey); ok {
				for _, w := range p.workers {
					if w.ID == targetID && w.IsHealthy() && atomic.LoadInt64(&w.InFlight) < p.maxConcurrencyPerWorker {
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

		// 2. Least-Busy Load Balancing (Lowest In-Flight requests first)
		var bestWorker *AccountWorker
		minInFlight := int64(999999)

		// Round-robin offset for fair distribution among identical load workers
		offset := atomic.AddUint64(&p.rrIndex, 1) % uint64(workersCount)

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

		if bestWorker != nil {
			atomic.AddInt64(&bestWorker.InFlight, 1)
			bestWorker.mu.Lock()
			bestWorker.LastUsed = time.Now()
			bestWorker.mu.Unlock()

			if affinityKey != "" {
				p.affinityMap.Store(affinityKey, bestWorker.ID)
			}
			p.mu.RUnlock()
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
		worker.Status = "active"
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "active")
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
		worker.Status = "cooldown"
		worker.CooldownUntil = time.Now().Add(45 * time.Second)
		log.Printf("⏳ [WorkerPool] Worker [%s] rate limited (429). In cooldown for 45s.", worker.ID)
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "rate_limited")
	} else if isAuthOrExpiry {
		worker.Status = "recovering"
		worker.CooldownUntil = time.Now().Add(15 * time.Second)
		log.Printf("🚨 [WorkerPool] Worker [%s] auth/session issue detected. Triggering cookie sync...", worker.ID)
		gemini.BroadcastCookieRefresh()
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "error")
	} else {
		_ = db.RecordAccountUsage(worker.ID, worker.CookiePath, "error")
	}
}

// ExecuteQueued dispatches a request through the worker pool with automatic retry on alternative workers
func (p *WorkerPool) ExecuteQueued(ctx context.Context, affinityKey string, op func(*gemini.GeminiClient) (*gemini.GeminiResponse, error)) (*gemini.GeminiResponse, error) {
	maxAttempts := 3
	p.mu.RLock()
	availableCount := len(p.workers)
	p.mu.RUnlock()

	if availableCount > 0 && availableCount < maxAttempts {
		maxAttempts = availableCount
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		worker, err := p.AcquireWorker(ctx, affinityKey)
		if err != nil {
			return nil, fmt.Errorf("failed to acquire worker from pool: %w", err)
		}

		resp, opErr := op(worker.Client)
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
		log.Printf("⚠️ [WorkerPool] Worker [%s] attempt %d/%d failed: %v. Retrying on another worker...", worker.ID, attempt, maxAttempts, opErr)

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
	worker, err := p.AcquireWorker(ctx, affinityKey)
	if err != nil {
		return fmt.Errorf("failed to acquire worker for streaming: %w", err)
	}
	defer p.ReleaseWorker(worker, nil)

	streamErr := op(worker.Client)
	if streamErr != nil {
		p.ReleaseWorker(worker, streamErr)
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
	TotalWorkers   int                      `json:"total_workers"`
	ActiveWorkers  int                      `json:"active_workers"`
	TotalInFlight  int64                    `json:"total_in_flight"`
	MaxConcurrency int64                    `json:"max_concurrency_per_worker"`
	Workers        []WorkerStats            `json:"workers"`
}

type WorkerStats struct {
	ID                     string `json:"id"`
	CookiePath             string `json:"cookie_path"`
	Status                 string `json:"status"`
	InFlight               int64  `json:"in_flight"`
	TotalServed            int64  `json:"total_served"`
	TotalErrors            int64  `json:"total_errors"`
	CooldownSecsRemaining  int    `json:"cooldown_seconds_remaining"`
	LastUsed               string `json:"last_used"`
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

		ws := WorkerStats{
			ID:                    w.ID,
			CookiePath:            w.CookiePath,
			Status:                w.Status,
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
