package api

import (
	"context"
	"fmt"
	"goapi/db"
	"goapi/gemini"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// HandleMusic handles /music generation endpoint
func HandleMusic(c fiber.Ctx) error {
	var req gemini.ChatRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
	}

	sessionID := req.UserID
	if sessionID == "" {
		sessionID = c.IP()
	}

	if req.NewChat {
		UserSessions.Delete(sessionID)
	}

	log.Printf("🎵 Music request from %s: %s", sessionID, req.Prompt)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := ExecuteWithFailover(sessionID, func(cl *gemini.GeminiClient) (*gemini.GeminiResponse, error) {
		return cl.AskWithTool(req.Prompt, "music_gen")
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			UserSessions.Delete(sessionID)
			return c.Status(504).JSON(fiber.Map{"error": "Music generation timed out."})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	if len(resp.Music) > 0 {
		var filteredMusic []gemini.MusicTrack
		for i, track := range resp.Music {
			if track.DownloadURL == "" || len(track.DownloadURL) < 50 {
				continue
			}
			ext := ".mp3"
			if strings.Contains(track.DownloadURL, ".mp4") {
				if i > 0 {
					continue
				}
				ext = ".mp4"
			}
			filename := fmt.Sprintf("music_%s_%d%s", resp.ResponseID, i, ext)
			if resp.ResponseID == "" {
				filename = fmt.Sprintf("music_%d_%d%s", time.Now().Unix(), i, ext)
			}
		dlClient, _ := GetOrCreateClient(sessionID)
		if err := DownloadAndClean(dlClient, track.DownloadURL, filename, "music"); err == nil {
				track.LocalPath = fmt.Sprintf("http://%s/output/%s", c.Host(), filename)
				log.Printf("🎵 Music downloaded: %s", track.Title)
				filteredMusic = append(filteredMusic, track)
				break
			} else {
				log.Printf("❌ Failed to download music track %d: %v", i, err)
			}
		}
		resp.Music = filteredMusic

		// Log to SQLite asynchronously and auto-export Excel
		userIP := c.IP()
		go func() {
			for _, track := range filteredMusic {
				fName := filepath.Base(track.LocalPath)
				_ = db.LogMediaGeneration("music", req.Prompt, fName, "./output/"+fName, track.LocalPath, "", resp.ResponseID)
			}
			_ = db.LogRequest("/music", "POST", userIP, 200, resp.Elapsed*1000)
			AutoExportAnalytics()
		}()
	}

	return c.JSON(resp)
}
