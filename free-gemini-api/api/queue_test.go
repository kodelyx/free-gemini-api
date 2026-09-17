package api

import (
	"context"
	"errors"
	"fmt"
	"goapi/gemini"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Helper to create a mock worker pool with dummy workers
func createMockPool(workerCount int, maxConcurrency int64) *WorkerPool {
	pool := &WorkerPool{
		maxConcurrencyPerWorker: maxConcurrency,
		workers:                 make([]*AccountWorker, 0, workerCount),
	}

	for i := 1; i <= workerCount; i++ {
		worker := &AccountWorker{
			ID:         fmt.Sprintf("account_%d", i),
			CookiePath: fmt.Sprintf("cookies/account_%d.json", i),
			Client:     &gemini.GeminiClient{}, // mock non-nil client
			Status:     "active",
			LastUsed:   time.Now(),
		}
		pool.workers = append(pool.workers, worker)
	}

	return pool
}

func TestWorkerPool_LeastBusyBalancing(t *testing.T) {
	pool := createMockPool(3, 2)
	ctx := context.Background()

	// Acquire worker 1
	w1, err := pool.AcquireWorker(ctx, "")
	if err != nil {
		t.Fatalf("Failed to acquire worker 1: %v", err)
	}

	// Acquire worker 2 - should pick a different worker because w1 has in-flight = 1
	w2, err := pool.AcquireWorker(ctx, "")
	if err != nil {
		t.Fatalf("Failed to acquire worker 2: %v", err)
	}

	if w1.ID == w2.ID {
		t.Errorf("Expected least-busy worker, but got same worker: %s", w1.ID)
	}

	// Acquire worker 3 - should pick the 3rd worker (in-flight = 0)
	w3, err := pool.AcquireWorker(ctx, "")
	if err != nil {
		t.Fatalf("Failed to acquire worker 3: %v", err)
	}

	if w3.ID == w1.ID || w3.ID == w2.ID {
		t.Errorf("Expected 3rd unique worker, got: %s", w3.ID)
	}

	// Clean up
	pool.ReleaseWorker(w1, nil)
	pool.ReleaseWorker(w2, nil)
	pool.ReleaseWorker(w3, nil)

	if atomic.LoadInt64(&w1.InFlight) != 0 || atomic.LoadInt64(&w2.InFlight) != 0 || atomic.LoadInt64(&w3.InFlight) != 0 {
		t.Errorf("Expected all in-flight counters to be 0 after release")
	}
}

func TestWorkerPool_StickySessionAffinity(t *testing.T) {
	pool := createMockPool(3, 5)
	ctx := context.Background()
	agentKey := "agent-coder-01"

	// First request with affinity key
	w1, err := pool.AcquireWorker(ctx, agentKey)
	if err != nil {
		t.Fatalf("Failed to acquire worker: %v", err)
	}
	firstWorkerID := w1.ID
	pool.ReleaseWorker(w1, nil)

	// Second request with SAME affinity key - MUST route to the same worker
	w2, err := pool.AcquireWorker(ctx, agentKey)
	if err != nil {
		t.Fatalf("Failed to acquire worker with affinity: %v", err)
	}
	defer pool.ReleaseWorker(w2, nil)

	if w2.ID != firstWorkerID {
		t.Errorf("Affinity failed: expected worker %s, got %s", firstWorkerID, w2.ID)
	}
}

func TestWorkerPool_CircuitBreaker_RateLimit(t *testing.T) {
	pool := createMockPool(2, 2)
	ctx := context.Background()

	// Acquire worker 1 and simulate a 429 Rate Limit error
	w1, err := pool.AcquireWorker(ctx, "")
	if err != nil {
		t.Fatalf("Failed to acquire worker: %v", err)
	}

	rateLimitErr := errors.New("Google API error: 429 resource_exhausted rate limit exceeded")
	pool.ReleaseWorker(w1, rateLimitErr)

	// Verify worker is in cooldown
	if w1.Status != "cooldown" {
		t.Errorf("Expected worker status 'cooldown', got '%s'", w1.Status)
	}
	if w1.IsHealthy() {
		t.Errorf("Expected worker to be unhealthy during cooldown")
	}

	// Next request MUST bypass worker 1 and pick worker 2
	w2, err := pool.AcquireWorker(ctx, "")
	if err != nil {
		t.Fatalf("Failed to acquire worker: %v", err)
	}
	defer pool.ReleaseWorker(w2, nil)

	if w2.ID == w1.ID {
		t.Errorf("Circuit breaker failed: got rate-limited worker %s", w1.ID)
	}
}

func TestWorkerPool_ExecuteQueued_FailoverRetry(t *testing.T) {
	pool := createMockPool(2, 2)
	ctx := context.Background()

	attemptCount := 0
	resp, err := pool.ExecuteQueued(ctx, "", func(cl *gemini.GeminiClient) (*gemini.GeminiResponse, error) {
		attemptCount++
		if attemptCount == 1 {
			// First worker fails with auth error
			return nil, errors.New("session expired servicelogin redirected")
		}
		// Second worker succeeds
		return &gemini.GeminiResponse{
			Text:           "Success from alternative worker",
			ConversationID: "c_test_123",
		}, nil
	})

	if err != nil {
		t.Fatalf("Expected failover to succeed, but got error: %v", err)
	}
	if resp == nil || resp.Text != "Success from alternative worker" {
		t.Errorf("Unexpected response: %v", resp)
	}
	if attemptCount != 2 {
		t.Errorf("Expected 2 attempts for failover, got %d", attemptCount)
	}
}

func TestWorkerPool_ConcurrentStress(t *testing.T) {
	pool := createMockPool(5, 3) // 5 workers, max 3 in-flight = 15 concurrency
	ctx := context.Background()
	concurrency := 20
	var wg sync.WaitGroup
	var successfulOps int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker, err := pool.AcquireWorker(ctx, fmt.Sprintf("agent_%d", id%5))
			if err != nil {
				return
			}
			// Simulate small work
			time.Sleep(10 * time.Millisecond)
			pool.ReleaseWorker(worker, nil)
			atomic.AddInt64(&successfulOps, 1)
		}(i)
	}

	wg.Wait()

	if successfulOps != int64(concurrency) {
		t.Errorf("Expected %d successful concurrent ops, got %d", concurrency, successfulOps)
	}

	// Verify all in-flight counters returned to 0
	stats := pool.GetStats()
	if stats.TotalInFlight != 0 {
		t.Errorf("Expected TotalInFlight = 0 after all operations, got %d", stats.TotalInFlight)
	}
}
