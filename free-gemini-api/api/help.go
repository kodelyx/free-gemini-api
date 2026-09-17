package api

import (
	"github.com/gofiber/fiber/v3"
)

// GetCLIHelpText returns the terminal-friendly CLI help and developer manual
func GetCLIHelpText() string {
	return `╔══════════════════════════════════════════════════════════════════════════════╗
║              ⚡ FREE GEMINI API — CLI HELP & DEVELOPER MANUAL                ║
║           HTTP/3 QUIC + Gemini 3.8 Flash + Needle 2 + Multi-Account          ║
╚══════════════════════════════════════════════════════════════════════════════╝

BASE URL:
  http://127.0.0.1:8001  (WebSocket Cookie Bridge: ws://127.0.0.1:9226)

CORE API ENDPOINTS:
  POST /v1/chat/completions   OpenAI-compatible chat & streaming completions
  GET  /v1/models             List supported models (gemini-3.8-flash, gemini-3.7-flash)
  GET  /v1/workers            Live worker pool metrics, active accounts & throughput
  GET  /api/pool              Alias for /v1/workers
  POST /chat                  Unified multimodal chat (Text, Images, MP4 screen recordings)
  POST /music                 AI Music generation via Gemini Lyria
  GET  /history               Query conversation history by conversation_id
  GET  /history/search?q=...  Full-text search across all stored conversations
  GET  /stats                 Real-time SQLite database & cost-saving metrics
  GET  /v1/analytics          Machine-readable JSON analytics export
  GET  /export/excel          Download comprehensive Excel intelligence report (.xlsx)
  GET  /health                Cookie health, TTL inspector & extension status
  POST /reset                 Reset an active agent/user session
  GET  /help                  Display this CLI developer manual

MULTI-AGENT HEADERS:
  X-Agent-ID: <id>            Sticky affinity binding (Keeps agent thread with same account)
  X-Session-ID: <id>          Custom session identifier
  X-Conversation-ID: <id>     Gemini server-side thread continuation

BUILT-IN TOOLS & CAPABILITIES:
  * Needle 2 Engine           Native OpenAI Tool / Function calling with zero-shot dispatch
  * Multi-Account Queue       Auto load balancing across 1 to 100+ accounts with circuit breaker
  * HTTP/3 QUIC & TLS         Chrome 152 JA4 fingerprint simulation with 0-RTT resumption
  * Multimodal Vision         Supports PNG, JPG, WebP, GIF, and MP4 video uploads
  * Gemini Music Gen          Generates AI instrumental and vocal audio tracks
  * Dual Analytics Export     Automated SQLite WAL persistence + JSON & Excel reports

SAMPLE USAGE:

  # 1. Quick Chat Completion:
  curl -s -X POST http://127.0.0.1:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Explain quantum computing in 1 line."}]}'

  # 2. Multi-Agent Sticky Affinity:
  curl -s -X POST http://127.0.0.1:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "X-Agent-ID: Agent-Alpha" \
    -d '{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Analyze competitor ads."}]}'

  # 3. Streaming Chat (SSE):
  curl -N -X POST http://127.0.0.1:8001/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{"model": "gemini-3.8-flash", "messages": [{"role": "user", "content": "Write a poem."}], "stream": true}'

  # 4. Check Cluster & Worker Pool Status:
  curl -s http://127.0.0.1:8001/v1/workers

  # 5. Generate AI Music (Gemini Lyria):
  curl -s -X POST http://127.0.0.1:8001/music \
    -H "Content-Type: application/json" \
    -d '{"prompt": "Upbeat futuristic synthwave beat with 80s bassline"}'

  # 6. Multimodal Vision / Local Image Analysis:
  curl -s -X POST http://127.0.0.1:8001/chat \
    -H "Content-Type: application/json" \
    -d '{"prompt": "Analyze this screenshot", "ref_image_path": "/path/to/screenshot.png"}'

  # 7. Search Past Conversations (SQLite):
  curl -s "http://127.0.0.1:8001/history/search?q=quantum"

  # 8. Download Excel Analytics Report:
  curl -s http://127.0.0.1:8001/export/excel -o analytics_report.xlsx

CLI COMMANDS & MODES:
  ./goapi                     Start the API server daemon (Port 8001 & WS 9226)
  ./goapi --help              Display this CLI help manual
  ./goapi --stats             Print instant SQLite database analytics in terminal
  ./goapi --export out.json   Export database analytics to JSON file
  ./goapi --export out.xlsx   Export database analytics to Excel spreadsheet
  ./goapi --mcp               Start as an MCP (Model Context Protocol) stdio server
                              Supported Tools: chat, generate_image, generate_video,
                              generate_music, check_health, reset_session
`
}

// HandleHelp serves CLI text or JSON help depending on request headers/query
func HandleHelp(c fiber.Ctx) error {
	format := c.Query("format")
	accept := string(c.Request().Header.Peek("Accept"))

	if format == "json" || (accept == "application/json" && format != "text") {
		return c.JSON(fiber.Map{
			"service":        "Free Gemini API",
			"engine":         "HTTP/3 QUIC + Needle 2 + WorkerPool",
			"standard_model": "gemini-3.8-flash",
			"port":           8001,
			"ws_port":        9226,
			"endpoints": []fiber.Map{
				{"method": "POST", "path": "/v1/chat/completions", "description": "OpenAI-compatible chat & streaming completions with tool calling"},
				{"method": "GET", "path": "/v1/models", "description": "List supported models (gemini-3.8-flash)"},
				{"method": "GET", "path": "/v1/workers", "description": "Live worker pool status, active accounts & concurrency"},
				{"method": "POST", "path": "/chat", "description": "Unified multimodal chat (Text, Images, MP4 screen recordings)"},
				{"method": "POST", "path": "/music", "description": "AI Music generation via Gemini Lyria"},
				{"method": "GET", "path": "/history", "description": "Query chat history from SQLite"},
				{"method": "GET", "path": "/history/search", "description": "Search past conversations"},
				{"method": "GET", "path": "/stats", "description": "Real-time SQLite database analytics"},
				{"method": "GET", "path": "/v1/analytics", "description": "Machine-readable JSON analytics export"},
				{"method": "GET", "path": "/export/excel", "description": "Download comprehensive Excel report (.xlsx)"},
				{"method": "GET", "path": "/health", "description": "Cookie health, TTL inspector & extension status"},
				{"method": "GET", "path": "/help", "description": "CLI Developer manual and help"},
			},
			"multi_agent_headers": []string{"X-Agent-ID", "X-Session-ID", "X-Conversation-ID"},
			"tools":               []string{"needle2_function_calling", "multimodal_vision", "gemini_music_lyria", "multi_account_queue"},
			"mcp_tools":           []string{"chat", "generate_image", "generate_video", "generate_music", "check_health", "reset_session"},
		})
	}

	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.SendString(GetCLIHelpText())
}
