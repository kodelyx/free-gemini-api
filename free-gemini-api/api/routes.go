package api

import (
	"goapi/db"
	"goapi/gemini"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registers all HTTP API routes onto the Fiber app instance
func RegisterRoutes(app *fiber.App) {
	// Health check endpoint
	app.Get("/health", func(c fiber.Ctx) error {
		cookieHealth := gemini.InspectAccountCookieHealth()
		return c.JSON(fiber.Map{
			"status":                   "online",
			"engine":                   "needle2 + free-gemini-api (pure go)",
			"cookie_health":            cookieHealth,
			"active_extension_workers": gemini.GetActiveWorkerCount(),
			"cookie_accounts_in_pool":  gemini.GetActiveAccountCount(),
		})
	})

	// Direct HTTP Cookie Sync endpoint (dual resilience fallback for Chrome Extension)
	app.Post("/api/sync-cookies", func(c fiber.Ctx) error {
		type SyncPayload struct {
			Cookies []gemini.CookieObject `json:"cookies"`
		}
		var payload SyncPayload
		if err := c.Bind().JSON(&payload); err != nil || len(payload.Cookies) == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid cookies payload"})
		}

		filePath, accID, err := gemini.ProcessAndSaveCookies(payload.Cookies)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(fiber.Map{
			"status":     "success",
			"account_id": accID,
			"file_path":  filePath,
			"count":      len(payload.Cookies),
		})
	})

	// OpenAI-compatible models list
	app.Get("/v1/models", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"object": "list",
			"data": []fiber.Map{
				{"id": "gemini-3.8-flash", "object": "model", "owned_by": "free-gemini-api"},
				{"id": "gemini-3.7-flash", "object": "model", "owned_by": "free-gemini-api"},
			},
		})
	})

	// OpenAI-compatible chat completions (with Native Needle 2 Tool Calling)
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
