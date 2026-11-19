package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// CachedImageCookies holds pre-fetched cookies for image/video downloads
var CachedImageCookies string

// OnCookiesUpdated is a callback triggered when new cookies are synced
var OnCookiesUpdated func()

// CookieHealthInfo represents the health and remaining lifetime of active cookies
type CookieHealthInfo struct {
	Status            string  `json:"status"` // "healthy", "expiring_soon", "expired", "no_cookies"
	MinTTLSeconds     float64 `json:"min_ttl_seconds"`
	MinTTLFormatted   string  `json:"min_ttl_formatted"`
	LastSyncedAt      string  `json:"last_synced_at"`
	ConnectedWorkers  int     `json:"connected_workers"`
	MonitoredAccounts int     `json:"monitored_accounts"`
}

var (
	activeClients     []*websocket.Conn
	activeClientsMu   sync.Mutex
	lastSyncMu        sync.Mutex
	lastSyncTimestamp time.Time
	lastBroadcastSync time.Time
)

// BroadcastCookieRefresh sends a trigger_sync message to all connected Chrome Extension clients
func BroadcastCookieRefresh() {
	activeClientsMu.Lock()
	defer activeClientsMu.Unlock()

	lastSyncMu.Lock()
	if time.Since(lastBroadcastSync) < 3*time.Second {
		lastSyncMu.Unlock()
		return // Coalesce rapid consecutive broadcasts
	}
	lastBroadcastSync = time.Now()
	lastSyncMu.Unlock()

	if len(activeClients) == 0 {
		log.Println("ℹ️ No Chrome Extension workers currently connected to receive trigger_sync")
		return
	}

	log.Printf("📢 Triggering memory sync to %d connected Chrome Extension worker(s)...", len(activeClients))

	payload := map[string]string{"type": "trigger_sync"}

	for i := len(activeClients) - 1; i >= 0; i-- {
		conn := activeClients[i]
		err := conn.WriteJSON(payload)
		if err != nil {
			log.Printf("⚠️ Failed to write to extension client: %v. Removing client.", err)
			conn.Close()
			activeClients = append(activeClients[:i], activeClients[i+1:]...)
		}
	}
}

// InspectAccountCookieHealth analyzes active account cookies and returns health status
func InspectAccountCookieHealth() CookieHealthInfo {
	accountFiles := GetAvailableAccountCookieFiles()
	info := CookieHealthInfo{
		Status:            "no_cookies",
		ConnectedWorkers:  GetActiveWorkerCount(),
		MonitoredAccounts: len(accountFiles),
	}

	lastSyncMu.Lock()
	if !lastSyncTimestamp.IsZero() {
		info.LastSyncedAt = lastSyncTimestamp.Format(time.RFC3339)
	}
	lastSyncMu.Unlock()

	if len(accountFiles) == 0 {
		return info
	}

	now := time.Now().Unix()
	minRemaining := float64(999999999)
	foundTargetCookie := false

	for _, fPath := range accountFiles {
		data, err := os.ReadFile(fPath)
		if err != nil {
			continue
		}
		var cookies []CookieObject
		if err := json.Unmarshal(data, &cookies); err != nil {
			continue
		}

		for _, ck := range cookies {
			if (ck.Name == "__Secure-1PSIDTS" || ck.Name == "__Secure-3PSIDTS" || ck.Name == "SIDCC") && ck.ExpirationDate > 1700000000 {
				rem := ck.ExpirationDate - float64(now)
				foundTargetCookie = true
				if rem < minRemaining {
					minRemaining = rem
				}
			}
		}
	}

	if !foundTargetCookie {
		info.Status = "healthy"
		info.MinTTLSeconds = 86400
		info.MinTTLFormatted = "> 24h"
		return info
	}

	info.MinTTLSeconds = minRemaining
	if minRemaining <= 0 {
		info.Status = "expired"
		info.MinTTLFormatted = "Expired"
	} else {
		hours := int(minRemaining) / 3600
		mins := (int(minRemaining) % 3600) / 60
		if hours > 0 {
			info.MinTTLFormatted = fmt.Sprintf("%dh %dm", hours, mins)
		} else {
			info.MinTTLFormatted = fmt.Sprintf("%dm", mins)
		}

		if minRemaining < 2*3600 {
			info.Status = "expiring_soon"
		} else {
			info.Status = "healthy"
		}
	}

	return info
}

// StartCookieWatchdog launches the passive background health monitor (on-demand sync mode)
func StartCookieWatchdog() {
	go func() {
		log.Println("🛡️ Passive Cookie Health Monitor started (logs status every 30m, on-demand sync only)")
		for {
			time.Sleep(30 * time.Minute)
			health := InspectAccountCookieHealth()
			log.Printf("📊 Passive Health Check: status=%s, remaining_ttl=%s, monitored_accounts=%d", health.Status, health.MinTTLFormatted, health.MonitoredAccounts)
			if health.Status == "expired" {
				log.Printf("🚨 Health Monitor: Cookie expired detected (%s). Requesting on-demand refresh...", health.MinTTLFormatted)
				BroadcastCookieRefresh()
			}

			// -- chrome-manage fast path (PRD Step 10 / Option A) ---------
			// The extension broadcast above only helps if a browser happens to
			// be open. This does not need one, and it also reacts to
			// expiring_soon rather than only expired. Rate-limited per profile
			// by shouldRequestRefresh (30 min), because expiring_soon is a
			// state and would otherwise fire on every tick.
			for _, acct := range InspectPerAccountCookieHealth() {
				if acct.Status != "expiring_soon" && acct.Status != "expired" {
					continue
				}
				if !acct.HasProfile {
					log.Printf("chrome-manage: account %s is %s (ttl=%s) but maps to no profile - skipped", acct.AccountID, acct.Status, acct.MinTTLFormatted)
					continue
				}
				if shouldRequestRefresh(acct.ProfileID) {
					log.Printf("chrome-manage: account %s is %s (ttl=%s) -> requesting refresh of profile %d", acct.AccountID, acct.Status, acct.MinTTLFormatted, acct.ProfileID)
					go callChromeManageRefresh(acct.ProfileID)
				}
			}
		}
	}()
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow Chrome Extension context
	},
}

type ExtensionMessage struct {
	Type    string         `json:"type"`
	Cookies []CookieObject `json:"cookies"`
}

// GetActiveWorkerCount returns number of connected Chrome Extension workers
func GetActiveWorkerCount() int {
	activeClientsMu.Lock()
	defer activeClientsMu.Unlock()
	return len(activeClients)
}

func getAccountIDFromCookies(cookies []CookieObject) string {
	for _, c := range cookies {
		if c.Name == "__Secure-1PSID" || c.Name == "SID" || c.Name == "HSID" {
			cleanVal := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(c.Value, "")
			if len(cleanVal) > 10 {
				return cleanVal[:10]
			}
			return cleanVal
		}
	}
	return "primary"
}

// GetAvailableAccountCookieFiles returns paths of all active account cookie files
func GetAvailableAccountCookieFiles() []string {
	files, err := filepath.Glob(filepath.Join("cookies", "account_*.json"))
	if err != nil || len(files) == 0 {
		defaultPath := filepath.Join("cookies", "cookies.json")
		if _, err := os.Stat(defaultPath); err == nil {
			return []string{defaultPath}
		}
		return nil
	}
	return files
}

// GetActiveAccountCount returns the number of distinct account profiles saved
func GetActiveAccountCount() int {
	return len(GetAvailableAccountCookieFiles())
}

// ProcessAndSaveCookies stores the received cookies into the account file, updates timestamps, and fires OnCookiesUpdated
//
// profileID, when non-nil and positive, selects the file: cookies/account_profile_<N>.json.
// That is the farm's own per-profile file, so a chrome-manage refresh UPDATES the account it
// already has instead of adding a second one for the same Google account.
//
// Without it the file is named from the cookie VALUE (getAccountIDFromCookies), which is not
// stable: __Secure-1PSID rotates, so nearly every refresh produced a hash matching no existing
// file and minted a brand-new account_<hash>.json. The pool globs account_*.json and loads each
// as its own worker, so a single Google account ended up served by several workers at once - and
// with max_concurrency_per_worker: 3 that multiplies the concurrent requests hitting that
// account, on a farm whose whole premise is avoiding Google lockouts.
// Measured 2026-09-27: 54 -> 55 workers after a single refresh.
//
// The Chrome extension has no profile id and keeps the old behaviour by passing nil.
func ProcessAndSaveCookies(cookies []CookieObject, profileID *int) (string, string, error) {
	accountID := getAccountIDFromCookies(cookies)
	if profileID != nil && *profileID > 0 {
		accountID = fmt.Sprintf("profile_%d", *profileID)
	}
	log.Printf("🍪 Received %d cookies for Account [%s]", len(cookies), accountID)

	lastSyncMu.Lock()
	lastSyncTimestamp = time.Now()
	lastSyncMu.Unlock()

	data, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal cookies: %w", err)
	}

	os.MkdirAll("cookies", 0755)

	accountFilePath := filepath.Join("cookies", fmt.Sprintf("account_%s.json", accountID))
	if err := os.WriteFile(accountFilePath, data, 0644); err != nil {
		return "", "", fmt.Errorf("failed to save %s: %w", accountFilePath, err)
	}
	log.Printf("💾 Stored account profile: %s", accountFilePath)

	// Refresh CachedImageCookies
	var parts []string
	for _, ck := range cookies {
		parts = append(parts, fmt.Sprintf("%s=%s", ck.Name, ck.Value))
	}
	CachedImageCookies = strings.Join(parts, "; ")

	// Trigger callback to reload active sessions
	if OnCookiesUpdated != nil {
		OnCookiesUpdated()
	}

	return accountFilePath, accountID, nil
}

// StartCookieWebSocketServer starts a local WebSocket server to receive cookies from the extension
func StartCookieWebSocketServer(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("❌ WS Upgrade failed: %v", err)
			return
		}

		activeClientsMu.Lock()
		activeClients = append(activeClients, conn)
		count := len(activeClients)
		activeClientsMu.Unlock()

		log.Printf("🔌 Chrome Extension connected to cookie bridge (Active Workers: %d)", count)

		defer func() {
			conn.Close()
			activeClientsMu.Lock()
			for i, c := range activeClients {
				if c == conn {
					activeClients = append(activeClients[:i], activeClients[i+1:]...)
					break
				}
			}
			remCount := len(activeClients)
			activeClientsMu.Unlock()
			log.Printf("🔌 Chrome Extension disconnected (Active Workers: %d)", remCount)
		}()

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var msg ExtensionMessage
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				log.Printf("❌ Failed to decode extension message: %v", err)
				continue
			}

			if msg.Type == "ping" {
				conn.WriteJSON(map[string]string{"type": "pong"})
				continue
			}

			if msg.Type == "cookies_payload" && len(msg.Cookies) > 0 {
				// nil profile id: the Chrome extension is not tied to a farm
				// profile, so this path keeps the cookie-value naming.
				_, _, err := ProcessAndSaveCookies(msg.Cookies, nil)
				if err != nil {
					log.Printf("❌ Failed to process cookies: %v", err)
				}
			}
		}
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("📡 Cookie WebSocket Server listening on %s", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ Cookie WebSocket Server failed: %v", err)
	}
}
