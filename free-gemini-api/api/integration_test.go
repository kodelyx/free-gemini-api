package api

import (
	"bytes"
	"encoding/json"
	"goapi/db"
	"goapi/gemini"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestE2E_RoutesAndTierTracking(t *testing.T) {
	// Initialize database
	_, err := db.InitDB()
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	// Create Fiber app with real registered routes
	app := fiber.New()
	RegisterRoutes(app)

	// Set up pool with 1 free worker and 1 subscription worker
	pool := GetWorkerPool()
	pool.mu.Lock()
	pool.workers = []*AccountWorker{
		{
			ID:         "test_free_account_1",
			CookiePath: "cookies/test_free.json",
			Status:     "active",
			Tier:       "free",
			LastUsed:   time.Now(),
		},
		{
			ID:         "test_sub_account_pro",
			CookiePath: "cookies/test_pro.json",
			Status:     "active",
			Tier:       "subscription",
			LastUsed:   time.Now(),
		},
	}
	pool.mu.Unlock()

	// 1. TEST GET /v1/models (Verify dynamic models list and tier segregation)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET /v1/models failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var modelsList struct {
		Object string                   `json:"object"`
		Data   []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsList); err != nil {
		t.Fatalf("Failed to parse /v1/models response: %v", err)
	}
	if len(modelsList.Data) < 3 {
		t.Fatalf("Expected at least 3 models, got %d", len(modelsList.Data))
	}

	foundFlash := false
	foundPro := false
	foundLite := false
	foundArgon := false
	for _, m := range modelsList.Data {
		id := m["id"].(string)
		tier := m["min_tier"].(string)
		if id == "gemini-3.8-flash" && tier == "free" {
			foundFlash = true
		}
		if id == "gemini-3.1-pro" && tier == "subscription" {
			foundPro = true
		}
		if id == "gemini-3.5-flash-lite" && tier == "free" {
			foundLite = true
		}
		if id == "gemini-4-argon" && tier == "free" {
			foundArgon = true
		}
	}
	if !foundFlash {
		t.Errorf("gemini-3.8-flash with tier free not found")
	}
	if !foundPro {
		t.Errorf("gemini-3.1-pro with tier subscription not found")
	}
	if !foundLite {
		t.Errorf("gemini-3.5-flash-lite with tier free not found")
	}
	if !foundArgon {
		t.Errorf("gemini-4-argon with tier free not found")
	}


	// 2. TEST POST /v1/models (Register custom model dynamically at runtime)
	newModelPayload := map[string]interface{}{
		"id":           "gemini-test-experimental-ultra",
		"display_name": "Gemini Ultra Experimental",
		"mode_id":      "9988aabbccddeeff",
		"min_tier":     "subscription",
		"capabilities": []string{"chat", "code", "ultra_reasoning"},
	}
	newModelBytes, _ := json.Marshal(newModelPayload)
	reqPostModel := httptest.NewRequest("POST", "/v1/models", bytes.NewReader(newModelBytes))
	reqPostModel.Header.Set("Content-Type", "application/json")
	respPostModel, err := app.Test(reqPostModel)
	if err != nil {
		t.Fatalf("POST /v1/models failed: %v", err)
	}
	if respPostModel.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK on POST /v1/models, got %d", respPostModel.StatusCode)
	}
	defer gemini.GetModelRegistry().Unregister("gemini-test-experimental-ultra")

	// Verify the newly added model resolves properly
	resolved := gemini.GetModelRegistry().Resolve("gemini-test-experimental-ultra")
	if resolved.ID != "gemini-test-experimental-ultra" || resolved.ModeID != "9988aabbccddeeff" {
		t.Fatalf("Custom model was not properly registered in ModelRegistry: %+v", resolved)
	}

	// 3. TEST GET /v1/workers (Verify separate Free vs Subscription worker tracking)
	reqWorkers := httptest.NewRequest("GET", "/v1/workers", nil)
	respWorkers, err := app.Test(reqWorkers)
	if err != nil {
		t.Fatalf("GET /v1/workers failed: %v", err)
	}
	var workerStats WorkerPoolStats
	workerBody, _ := io.ReadAll(respWorkers.Body)
	if err := json.Unmarshal(workerBody, &workerStats); err != nil {
		t.Fatalf("Failed to parse /v1/workers: %v", err)
	}
	if workerStats.FreeWorkers != 1 {
		t.Errorf("Expected 1 free worker, got %d", workerStats.FreeWorkers)
	}
	if workerStats.SubscriptionWorkers != 1 {
		t.Errorf("Expected 1 subscription worker, got %d", workerStats.SubscriptionWorkers)
	}

	// 4. TEST Database Logging & Stats Breakdown
	_ = db.LogMessageWithTier("conv_e2e_1", "user_free_ip", "user", "What is Go?", "gemini-3.8-flash", 50, "free", "test_free_account_1")
	_ = db.LogMessageWithTier("conv_e2e_1", "user_free_ip", "assistant", "Go is an open-source programming language.", "gemini-3.8-flash", 65, "free", "test_free_account_1")
	_ = db.LogMessageWithTier("conv_e2e_2", "user_sub_ip", "user", "Write an OS kernel in Rust.", "gemini-3.1-pro", 120, "subscription", "test_sub_account_pro")
	_ = db.LogMessageWithTier("conv_e2e_2", "user_sub_ip", "assistant", "Here is a basic microkernel skeleton...", "gemini-3.1-pro", 450, "subscription", "test_sub_account_pro")

	// 5. TEST GET /stats (Verify separate free vs subscription analytics)
	reqStats := httptest.NewRequest("GET", "/stats", nil)
	respStats, err := app.Test(reqStats)
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	statsBody, _ := io.ReadAll(respStats.Body)
	var statsData map[string]interface{}
	if err := json.Unmarshal(statsBody, &statsData); err != nil {
		t.Fatalf("Failed to parse /stats: %v", err)
	}

	tierBD, ok := statsData["tier_breakdown"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing tier_breakdown in /stats response: %s", string(statsBody))
	}
	freeInfo := tierBD["free"].(map[string]interface{})
	subInfo := tierBD["subscription"].(map[string]interface{})

	if freeInfo["messages"].(float64) < 2 {
		t.Errorf("Expected >= 2 free messages, got %v", freeInfo["messages"])
	}
	if subInfo["messages"].(float64) < 2 {
		t.Errorf("Expected >= 2 subscription messages, got %v", subInfo["messages"])
	}

	// 6. TEST GET /v1/analytics (JSON export)
	reqAnalytics := httptest.NewRequest("GET", "/v1/analytics", nil)
	respAnalytics, err := app.Test(reqAnalytics)
	if err != nil {
		t.Fatalf("GET /v1/analytics failed: %v", err)
	}
	if respAnalytics.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK on /v1/analytics, got %d", respAnalytics.StatusCode)
	}
}
