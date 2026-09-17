package main

import (
	"fmt"
	"goapi/api"
	"goapi/db"
	"goapi/gemini"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(".env", "config.env"); err != nil {
		log.Println("⚠️  No .env file found, using system environment variables")
	}

	// Check if invoked with --help or help flag
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help") {
		fmt.Println(api.GetCLIHelpText())
		return
	}

	// Check if invoked as MCP stdio server
	if len(os.Args) > 1 && (os.Args[1] == "--mcp" || os.Args[1] == "-mcp" || os.Args[1] == "mcp") {
		api.RunMCPServer()
		return
	}

	// Initialize SQLite Database
	if _, err := db.InitDB(); err != nil {
		log.Printf("⚠️ SQLite initialization warning: %v", err)
	}

	// Check if invoked as CLI stats inspector
	if len(os.Args) > 1 && (os.Args[1] == "stats" || os.Args[1] == "--stats") {
		stats, err := db.GetSystemStats()
		if err != nil {
			log.Fatalf("❌ Failed to read stats: %v", err)
		}
		accounts, _ := db.GetAccounts()
		fmt.Println("\n╔══════════════════════════════════════════════════════════╗")
		fmt.Println("║       📊 FREE GEMINI API - SYSTEM & DB ANALYTICS         ║")
		fmt.Println("╠══════════════════════════════════════════════════════════╣")
		fmt.Printf("║  Database File:    %-37s ║\n", fmt.Sprintf("%v", stats["database_file"]))
		fmt.Printf("║  Database Engine:  %-37s ║\n", fmt.Sprintf("%v", stats["engine"]))
		fmt.Printf("║  Primary Model:    %-37s ║\n", "Google Gemini 3.8 Flash")
		fmt.Printf("║  Total Messages:   %-37v ║\n", stats["total_messages"])
		fmt.Printf("║  Total Tokens:     %-37v ║\n", stats["total_tokens"])
		fmt.Printf("║  Total Media:      %-37v ║\n", stats["total_media"])
		fmt.Printf("║  Total API Calls:  %-37v ║\n", stats["total_requests"])
		fmt.Printf("║  Est. Cost Saved:  %-37s ║\n", fmt.Sprintf("%s (%s) [100%% FREE]", stats["cost_saved_usd_formatted"], stats["cost_saved_inr_formatted"]))
		fmt.Printf("║  Tracked Accounts: %-37d ║\n", len(accounts))
		fmt.Println("╚══════════════════════════════════════════════════════════╝")
		return
	}

	// Check if invoked as Analytics CLI exporter
	if len(os.Args) > 1 && (os.Args[1] == "export" || os.Args[1] == "--export") {
		targetFile := ""
		if len(os.Args) > 2 {
			targetFile = os.Args[2]
		}
		if strings.HasSuffix(targetFile, ".json") {
			outJSON, _, err := api.ExportAnalyticsToJSON(targetFile)
			if err != nil {
				log.Fatalf("❌ JSON Export failed: %v", err)
			}
			log.Printf("✅ JSON Analytics exported successfully: %s", outJSON)
		} else if strings.HasSuffix(targetFile, ".xlsx") {
			outXlsx, err := api.ExportAnalyticsToExcel(targetFile)
			if err != nil {
				log.Fatalf("❌ Excel Export failed: %v", err)
			}
			log.Printf("✅ Excel Analytics exported successfully: %s", outXlsx)
		} else {
			outXlsx, err := api.ExportAnalyticsToExcel(targetFile)
			if err != nil {
				log.Fatalf("❌ Excel Export failed: %v", err)
			}
			outJSON, _, _ := api.ExportAnalyticsToJSON("")
			log.Printf("✅ Dual Analytics exported successfully:\n   📊 Excel: %s\n   ⚡ JSON:  %s", outXlsx, outJSON)
		}
		return
	}

	// Start Chrome Extension WebSocket bridge & Multi-Account Worker Pool
	api.StartWebSocketBridge()
	gemini.StartCookieWatchdog()
	api.InitWorkerPool()

	// Initialize Fiber web application
	app := fiber.New(fiber.Config{
		AppName:   "Gemini Go API (HTTP/3 QUIC + Gemini 3.8 + Needle 2)",
		BodyLimit: 50 * 1024 * 1024, // 50MB
	})

	// Middlewares
	app.Use(cors.New())
	app.Use(logger.New())
	app.Use(func(c fiber.Ctx) error {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("⚠️ RECOVERED from panic: %v", r)
				c.Status(500).JSON(fiber.Map{"error": "Internal Server Error - Recovered"})
			}
		}()
		return c.Next()
	})

	// Register all HTTP routes
	api.RegisterRoutes(app)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8001"
	}

	// Setup graceful shutdown on SIGINT / SIGTERM
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		log.Println("\n⚠️ Interrupted by user (SIGINT/SIGTERM)! Finalizing in-flight requests and shutting down cleanly...")
		if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
			log.Printf("⚠️ Fiber graceful shutdown error: %v", err)
		}
	}()

	log.Printf("🚀 Free Gemini API Server starting on port :%s (HTTP/3 QUIC enabled)...", port)
	if err := app.Listen(":" + port); err != nil {
		log.Printf("Server stopped: %v", err)
	}
	log.Println("🏁 Free Gemini API server exited cleanly.")
}
