package api

import (
	"goapi/db"
	"goapi/gemini"
	"log"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// bridgeAuthorized reports whether a request may read cookie-pool internals or
// write into the pool (issue #1). The shared secret is accepted in the
// X-Bridge-Token header, as an Authorization: Bearer value, or as ?token= for
// clients that cannot set headers.
func bridgeAuthorized(c fiber.Ctx) bool {
	if gemini.BridgeTokenDisabled() {
		return true
	}
	token := strings.TrimSpace(c.Get(gemini.BridgeTokenHeader))
	if token == "" {
		if auth := strings.TrimSpace(c.Get("Authorization")); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
			token = strings.TrimSpace(auth[7:])
		}
	}
	if token == "" {
		token = strings.TrimSpace(c.Query(gemini.BridgeTokenQuery))
	}
	return gemini.BridgeTokenMatches(token)
}

// RegisterRoutes registers all HTTP API routes onto the Fiber app instance
func RegisterRoutes(app *fiber.App) {
	// CLI Help & Developer Manual
	app.Get("/", HandleHelp)
	app.Get("/help", HandleHelp)

	// Health check endpoint
	//
	// Unauthenticated callers get reachability only. The pool internals below
	// used to be world-readable, which is what made "find any host answering
	// {"status":"online"} on :8001/health" a useful attack: it fingerprinted a
	// cookie farm and confirmed the port was live before harvesting from it.
	// Clients that present the bridge secret (or an operator who disabled the
	// gate) still get the full payload.
	app.Get("/health", func(c fiber.Ctx) error {
		resp := fiber.Map{
			"status": "online",
			"engine": "arranger + free-gemini-api (pure go)",
		}

		// Challenge-response identity check: a client that has not yet decided to
		// trust this host can ask it to prove it holds the bridge secret without
		// disclosing that secret to the host. See gemini.BridgeProof.
		if nonce := strings.TrimSpace(c.Query("nonce")); gemini.ValidBridgeNonce(nonce) {
			resp["bridge_proof"] = gemini.BridgeProof(nonce)
		}

		if !bridgeAuthorized(c) {
			resp["bridge_auth"] = "required"
			return c.JSON(resp)
		}
		resp["bridge_auth"] = "ok"
		resp["bridge_token_fingerprint"] = gemini.BridgeTokenFingerprint()
		resp["cookie_health"] = gemini.InspectAccountCookieHealth()
		resp["active_extension_workers"] = gemini.GetActiveWorkerCount()
		resp["cookie_accounts_in_pool"] = gemini.GetActiveAccountCount()
		return c.JSON(resp)
	})

	// Live Worker Pool Metrics & Multi-Account concurrency stats
	app.Get("/v1/workers", func(c fiber.Ctx) error {
		return c.JSON(GetWorkerPool().GetStats())
	})
	app.Get("/api/pool", func(c fiber.Ctx) error {
		return c.JSON(GetWorkerPool().GetStats())
	})

	// Direct HTTP Cookie Sync endpoint (dual resilience fallback for Chrome Extension)
	//
	// Requires the bridge shared secret (issue #1): this endpoint writes Google
	// session cookies straight into the worker pool from any host that can
	// reach the port, so unauthenticated access here is a full account takeover
	// primitive, not just an information leak.
	app.Post("/api/sync-cookies", func(c fiber.Ctx) error {
		if !bridgeAuthorized(c) {
			log.Printf("🚫 Rejected unauthenticated POST /api/sync-cookies from %s", c.IP())
			return c.Status(401).JSON(fiber.Map{
				"error": "unauthorized — send the cookie bridge token in the " + gemini.BridgeTokenHeader + " header",
			})
		}

		type SyncPayload struct {
			Cookies []gemini.CookieObject `json:"cookies"`
			// ProfileID is the farm profile this jar belongs to. chrome-manage
			// sends it; binding only `cookies` dropped it on the floor, which
			// made ProcessAndSaveCookies fall back to naming the file from the
			// cookie VALUE. Because __Secure-1PSID rotates, that minted a NEW
			// account_<hash>.json on nearly every refresh instead of updating
			// account_profile_<N>.json - so the pool grew a duplicate worker for
			// the same Google account each time (measured 2026-09-27: 54 -> 55
			// after a single refresh).
			//
			// Nil for the Chrome extension, which has no profile id and must
			// keep the old hash naming.
			ProfileID *int `json:"profile_id"`
		}
		var payload SyncPayload
		if err := c.Bind().JSON(&payload); err != nil || len(payload.Cookies) == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid cookies payload"})
		}

		filePath, accID, err := gemini.ProcessAndSaveCookies(payload.Cookies, payload.ProfileID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		resp := fiber.Map{
			"status":     "success",
			"account_id": accID,
			"file_path":  filePath,
			"count":      len(payload.Cookies),
		}
		if payload.ProfileID != nil {
			resp["profile_id"] = *payload.ProfileID
		}
		return c.JSON(resp)
	})

	// OpenAI-compatible models list (dynamically loaded from Model Registry)
	app.Get("/v1/models", func(c fiber.Ctx) error {
		reg := gemini.GetModelRegistry()
		models := reg.GetAll()
		var list []fiber.Map
		for _, m := range models {
			list = append(list, fiber.Map{
				"id":           m.ID,
				"object":       "model",
				"created":      1710000000,
				"owned_by":     "google",
				"display_name": m.DisplayName,
				"description":  m.Description,
				"min_tier":     m.MinTier,
				"mode_id":      m.ModeID,
				"is_default":   m.IsDefault,
				"capabilities": m.Capabilities,
				"aliases":      m.Aliases,
				"source":       m.Source,
			})
		}
		return c.JSON(fiber.Map{
			"object": "list",
			"data":   list,
		})
	})

	// Dynamic registration/addition of custom models and mode hashes at runtime
	app.Post("/v1/models", func(c fiber.Ctx) error {
		var cfg gemini.ModelConfig
		if err := c.Bind().JSON(&cfg); err != nil || cfg.ID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid model config payload. 'id' is required."})
		}
		if cfg.DisplayName == "" {
			cfg.DisplayName = cfg.ID
		}
		if cfg.MinTier == "" {
			cfg.MinTier = "free"
		}
		if cfg.Source == "" {
			cfg.Source = "custom"
		}
		gemini.GetModelRegistry().Register(cfg)
		return c.JSON(fiber.Map{
			"status":  "success",
			"message": "Model registered successfully",
			"model":   cfg,
		})
	})

	// Autonomous Model Discovery across all connected accounts
	app.Post("/v1/models/discover", func(c fiber.Ctx) error {
		res, err := GetWorkerPool().DiscoverModelsFromWorkers()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(res)
	})

	// OpenAI-compatible chat completions (with Native Tool Calling & Output Arranger)
	app.Post("/v1/chat/completions", HandleOpenAIChatCompletions)

	// Unified chat endpoint (text, images, screen recordings, ref videos)
	app.Post("/chat", HandleUnifiedChat)

	// Gemini Music generation
	app.Post("/music", HandleMusic)

	// Reset active session
	app.Post("/reset", func(c fiber.Ctx) error {
		type ResetRequest struct {
			UserID string `json:"user_id"`
		}
		var req ResetRequest
		c.Bind().JSON(&req)

		sessionID := req.UserID
		if sessionID == "" {
			sessionID = c.IP()
		}

		if _, ok := UserSessions.Load(sessionID); ok {
			UserSessions.Delete(sessionID)
			return c.JSON(fiber.Map{"status": "success", "message": "Session reset"})
		}

		return c.JSON(fiber.Map{"status": "error", "message": "No active session"})
	})

	// Session status & health info
	app.Get("/status", func(c fiber.Ctx) error {
		sessionID := c.Query("user_id")
		if sessionID == "" {
			sessionID = c.IP()
		}

		if clientRaw, ok := UserSessions.Load(sessionID); ok {
			client := clientRaw.(*gemini.GeminiClient)
			return c.JSON(fiber.Map{
				"session_id":      sessionID,
				"initialized":     client.IsInitialized,
				"conversation_id": client.ConversationID,
				"expired":         false,
			})
		}
		return c.JSON(fiber.Map{"session_id": sessionID, "active": false})
	})

	// SQLite History & Database endpoints
	app.Get("/history", func(c fiber.Ctx) error {
		convID := c.Query("conversation_id")
		limitStr := c.Query("limit")
		limit := 50
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}
		history, err := db.GetRecentMessages(convID, limit)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"conversation_id": convID, "messages": history})
	})

	app.Get("/media", func(c fiber.Ctx) error {
		if db.DB == nil {
			return c.JSON(fiber.Map{"media": []any{}})
		}
		rows, err := db.DB.Query(`SELECT id, type, prompt, file_name, file_path, url, created_at FROM media_generations ORDER BY id DESC LIMIT 50`)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		defer rows.Close()

		var mediaList []fiber.Map
		for rows.Next() {
			var id int
			var mType, prompt, fileName, filePath, urlStr, createdAt string
			if err := rows.Scan(&id, &mType, &prompt, &fileName, &filePath, &urlStr, &createdAt); err == nil {
				mediaList = append(mediaList, fiber.Map{
					"id":         id,
					"type":       mType,
					"prompt":     prompt,
					"file_name":  fileName,
					"file_path":  filePath,
					"url":        urlStr,
					"created_at": createdAt,
				})
			}
		}
		return c.JSON(fiber.Map{"media": mediaList})
	})

	// Search chat history endpoint
	app.Get("/history/search", func(c fiber.Ctx) error {
		q := c.Query("q")
		limitStr := c.Query("limit")
		limit := 10
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}
		res, err := db.SearchMessages(q, limit)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"query": q, "results": res})
	})

	// Live SQLite stats endpoint
	app.Get("/stats", func(c fiber.Ctx) error {
		stats, err := db.GetSystemStats()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		stats["active_accounts"] = gemini.GetActiveAccountCount()
		stats["active_workers"] = gemini.GetActiveWorkerCount()
		stats["cookie_health"] = gemini.InspectAccountCookieHealth()
		return c.JSON(stats)
	})

	// Machine-readable JSON Analytics endpoint (Direct REST API)
	app.Get("/v1/analytics", HandleJSONExport)
	app.Get("/export/json", HandleJSONExport)

	// Excel Analytics & Intelligence Exporter
	app.Get("/export/excel", HandleExcelExport)

	// Static generated output files (images, videos, music)
	app.Get("/output/*", func(c fiber.Ctx) error {
		return c.SendFile("./output/" + c.Params("*"))
	})
}
