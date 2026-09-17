package api

import (
	"context"
	"fmt"
	"goapi/gemini"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	UserSessions sync.Map
)

const CookiesFile = "cookies/cookies.json"

func DownloadAndClean(client *gemini.GeminiClient, urlStr, filename, fileType string) error {
	tempDir := "./output/.temp"
	os.MkdirAll(tempDir, 0755)
	os.MkdirAll("./output", 0755)

	tempPath := filepath.Join(tempDir, filename)
	outputPath := filepath.Join("./output", filename)

	if err := client.DownloadFile(urlStr, tempPath); err != nil {
		os.RemoveAll(tempDir)
		return err
	}

	var moveErr error
	if err := os.Rename(tempPath, outputPath); err != nil {
		input, err := os.ReadFile(tempPath)
		if err != nil {
			moveErr = err
		} else if err := os.WriteFile(outputPath, input, 0644); err != nil {
			moveErr = err
		}
	}

	os.RemoveAll(tempDir)
	if moveErr != nil {
		return moveErr
	}

	log.Printf("✨ Cleaned file moved to output: %s", outputPath)
	return nil
}

func GetOrCreateClient(sessionID string) (*gemini.GeminiClient, error) {
	if available := gemini.GetAvailableAccountCookieFiles(); len(available) > 0 {
		return GetOrCreateClientWithFile(sessionID, available[0])
	}
	return GetOrCreateClientWithFile(sessionID, CookiesFile)
}

func GetOrCreateClientWithFile(sessionID, cookieFilePath string) (*gemini.GeminiClient, error) {
	// If the requested path doesn't exist, check if an account file is available
	if _, err := os.Stat(cookieFilePath); os.IsNotExist(err) {
		if available := gemini.GetAvailableAccountCookieFiles(); len(available) > 0 {
			cookieFilePath = available[0]
		}
	}

	cacheKey := fmt.Sprintf("%s_%s", sessionID, cookieFilePath)
	if client, ok := UserSessions.Load(cacheKey); ok {
		return client.(*gemini.GeminiClient), nil
	}

	if _, err := os.Stat(cookieFilePath); os.IsNotExist(err) {
		log.Printf("⚠️ %s not found. Requesting Chrome Extension to proactively sync cookies...", cookieFilePath)
		gemini.BroadcastCookieRefresh()

		log.Println("⏳ Sleeping 5 seconds waiting for extension to sync cookies...")
		time.Sleep(5 * time.Second)

		if available := gemini.GetAvailableAccountCookieFiles(); len(available) > 0 {
			cookieFilePath = available[0]
		} else if _, err := os.Stat(cookieFilePath); os.IsNotExist(err) {
			return nil, fmt.Errorf("No account cookies found. Please ensure Chrome Extension is connected and syncs cookies first.")
		}
	}

	client, err := gemini.NewClient(cookieFilePath)
	if err != nil {
		return nil, err
	}

	UserSessions.Store(cacheKey, client)
	log.Printf("New session created for: %s (using %s)", sessionID, cookieFilePath)
	return client, nil
}

// ExecuteWithFailover executes an operation across all active worker accounts using the high-performance WorkerPool
func ExecuteWithFailover(sessionID string, op func(client *gemini.GeminiClient) (*gemini.GeminiResponse, error)) (*gemini.GeminiResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return GetWorkerPool().ExecuteQueued(ctx, sessionID, op)
}

func StartWebSocketBridge() {
	gemini.OnCookiesUpdated = func() {
		log.Println("♻️  Extension pushed new cookies. Reloading active sessions across all workers...")
		UserSessions.Range(func(key, value interface{}) bool {
			client := value.(*gemini.GeminiClient)
			sessionID := key.(string)

			if err := client.ReloadSession(); err != nil {
				log.Printf("❌ Failed to reload session for %s: %v", sessionID, err)
			} else {
				log.Printf("✅ Session %s refreshed", sessionID)
			}
			return true
		})

		// Reload the multi-account WorkerPool
		GetWorkerPool().ReloadPool()
	}

	wsPortStr := os.Getenv("WS_PORT")
	if wsPortStr == "" {
		wsPortStr = "9226"
	}
	wsPort, err := strconv.Atoi(wsPortStr)
	if err != nil {
		wsPort = 9226
	}

	go gemini.StartCookieWebSocketServer(wsPort)
}
