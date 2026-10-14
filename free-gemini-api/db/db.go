package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	DB   *sql.DB
	once sync.Once
	dbMu sync.Mutex
)

const DBFileName = "data/gemini.db"

// InitDB initializes SQLite database connection and auto-migrates all tables
func InitDB() (*sql.DB, error) {
	var initErr error
	once.Do(func() {
		dir := filepath.Dir(DBFileName)
		if err := os.MkdirAll(dir, 0755); err != nil {
			initErr = err
			return
		}

		database, err := sql.Open("sqlite", DBFileName+"?_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=busy_timeout(5000)&_pragma=temp_store(memory)")
		if err != nil {
			initErr = err
			return
		}

		// Set connection pool
		database.SetMaxOpenConns(25)
		database.SetMaxIdleConns(10)
		database.SetConnMaxLifetime(time.Hour)

		// Create tables
		schema := `
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT,
			user_id TEXT,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			model TEXT,
			tokens INTEGER DEFAULT 0,
			account_tier TEXT DEFAULT 'free',
			account_id TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
		CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id);
		CREATE INDEX IF NOT EXISTS idx_messages_tier ON messages(account_tier);
		CREATE INDEX IF NOT EXISTS idx_messages_model ON messages(model);

		CREATE TABLE IF NOT EXISTS media_generations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL, -- 'image', 'video', 'music'
			prompt TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			url TEXT,
			aspect_ratio TEXT,
			response_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_media_type ON media_generations(type);

		CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			endpoint TEXT NOT NULL,
			method TEXT NOT NULL,
			user_ip TEXT,
			status_code INTEGER,
			elapsed_ms REAL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id TEXT UNIQUE NOT NULL,
			cookie_file TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			tier TEXT DEFAULT 'free',
			total_requests INTEGER DEFAULT 0,
			last_used_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		`

		if _, err := database.Exec(schema); err != nil {
			initErr = err
			return
		}

		// Auto-migrate existing database tables
		_, _ = database.Exec(`ALTER TABLE messages ADD COLUMN account_tier TEXT DEFAULT 'free'`)
		_, _ = database.Exec(`ALTER TABLE messages ADD COLUMN account_id TEXT DEFAULT ''`)
		_, _ = database.Exec(`ALTER TABLE accounts ADD COLUMN tier TEXT DEFAULT 'free'`)

		DB = database
		log.Println("🗄️  SQLite Database initialized successfully at data/gemini.db (WAL Mode)")
	})

	return DB, initErr
}

// LogMessage saves a chat turn into SQLite (legacy fallback)
func LogMessage(conversationID, userID, role, content, model string, tokens int) error {
	return LogMessageWithTier(conversationID, userID, role, content, model, tokens, "free", "")
}

// LogMessageWithTier saves a chat turn into SQLite with account tier and worker ID
func LogMessageWithTier(conversationID, userID, role, content, model string, tokens int, accountTier, accountID string) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if accountTier == "" {
		accountTier = "free"
	}
	query := `INSERT INTO messages (conversation_id, user_id, role, content, model, tokens, account_tier, account_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, conversationID, userID, role, content, model, tokens, accountTier, accountID)
	return err
}

// LogMediaGeneration records a generated image, video or music track
func LogMediaGeneration(mediaType, prompt, fileName, filePath, url, aspectRatio, responseID string) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	query := `INSERT INTO media_generations (type, prompt, file_name, file_path, url, aspect_ratio, response_id) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, mediaType, prompt, fileName, filePath, url, aspectRatio, responseID)
	return err
}

// LogRequest records an API request log
func LogRequest(endpoint, method, userIP string, statusCode int, elapsedMs float64) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	query := `INSERT INTO request_logs (endpoint, method, user_ip, status_code, elapsed_ms) VALUES (?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, endpoint, method, userIP, statusCode, elapsedMs)
	return err
}

// GetRecentMessages retrieves conversation history
func GetRecentMessages(conversationID string, limit int) ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT role, content, model, created_at FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var role, content, model, createdAt string
		if err := rows.Scan(&role, &content, &model, &createdAt); err == nil {
			result = append(result, map[string]any{
				"role":       role,
				"content":    content,
				"model":      model,
				"created_at": createdAt,
			})
		}
	}
	return result, nil
}

// SearchMessages searches past chat history across all messages
func SearchMessages(query string, limit int) ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	searchPattern := "%" + query + "%"
	rows, err := DB.Query(`SELECT id, COALESCE(role, ''), COALESCE(content, ''), COALESCE(model, ''), COALESCE(created_at, '') FROM messages WHERE content LIKE ? ORDER BY id DESC LIMIT ?`, searchPattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id int
		var role, content, model, createdAt string
		if err := rows.Scan(&id, &role, &content, &model, &createdAt); err == nil {
			result = append(result, map[string]any{
				"id":         id,
				"role":       role,
				"content":    content,
				"model":      model,
				"created_at": createdAt,
			})
		}
	}
	return result, nil
}

// GetMediaByPrompt retrieves generated media items matching a filter/query
func GetMediaByPrompt(mediaType, query string, limit int) ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	sqlQuery := `SELECT id, type, prompt, file_name, url, created_at FROM media_generations WHERE 1=1`
	var args []any
	if mediaType != "" && mediaType != "all" {
		sqlQuery += ` AND type = ?`
		args = append(args, mediaType)
	}
	if query != "" {
		sqlQuery += ` AND prompt LIKE ?`
		args = append(args, "%"+query+"%")
	}
	sqlQuery += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := DB.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id int
		var mType, prompt, fileName, urlStr, createdAt string
		if err := rows.Scan(&id, &mType, &prompt, &fileName, &urlStr, &createdAt); err == nil {
			result = append(result, map[string]any{
				"id":         id,
				"type":       mType,
				"prompt":     prompt,
				"file_name":  fileName,
				"url":        urlStr,
				"created_at": createdAt,
			})
		}
	}
	return result, nil
}

// GetSystemStats returns aggregate statistics, official API cost savings, and separate Free vs Subscription metrics
func GetSystemStats() (map[string]any, error) {
	if DB == nil {
		return map[string]any{"status": "database_not_connected"}, nil
	}
	var totalMessages, totalMedia, totalRequests, totalTokens int
	var totalImages, totalVideos, totalMusic int

	_ = DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&totalMessages)
	_ = DB.QueryRow(`SELECT COALESCE(SUM(tokens), 0) FROM messages`).Scan(&totalTokens)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM media_generations`).Scan(&totalMedia)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM media_generations WHERE type='image'`).Scan(&totalImages)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM media_generations WHERE type='video'`).Scan(&totalVideos)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM media_generations WHERE type='music'`).Scan(&totalMusic)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&totalRequests)

	// Free Tier vs Subscription Tier Breakdown
	var freeMessages, subMessages int
	var freeTokens, subTokens int
	_ = DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE account_tier = 'free' OR account_tier IS NULL OR account_tier = ''`).Scan(&freeMessages)
	_ = DB.QueryRow(`SELECT COALESCE(SUM(tokens), 0) FROM messages WHERE account_tier = 'free' OR account_tier IS NULL OR account_tier = ''`).Scan(&freeTokens)
	_ = DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE account_tier = 'subscription'`).Scan(&subMessages)
	_ = DB.QueryRow(`SELECT COALESCE(SUM(tokens), 0) FROM messages WHERE account_tier = 'subscription'`).Scan(&subTokens)

	// Models breakdown by model and tier
	modelStats := make(map[string]map[string]int)
	mRows, err := DB.Query(`SELECT COALESCE(model, 'unknown'), COALESCE(account_tier, 'free'), COUNT(*), COALESCE(SUM(tokens), 0) FROM messages GROUP BY model, account_tier`)
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var mName, mTier string
			var mCount, mTok int
			if err := mRows.Scan(&mName, &mTier, &mCount, &mTok); err == nil {
				key := fmt.Sprintf("%s (%s)", mName, mTier)
				modelStats[key] = map[string]int{
					"messages": mCount,
					"tokens":   mTok,
				}
			}
		}
	}

	// Cost savings:
	// Free Tier Flash tokens: $0.50 / 1M tokens ($0.0000005/token)
	// Subscription Tier Pro tokens: $2.50 / 1M tokens ($0.0000025/token)
	freeCostTokensUSD := float64(freeTokens) * 0.0000005
	subCostTokensUSD := float64(subTokens) * 0.0000025
	mediaCostUSD := (float64(totalImages) * 0.030) + (float64(totalVideos) * 1.200) + (float64(totalMusic) * 0.080)

	costSavedUSD := freeCostTokensUSD + subCostTokensUSD + mediaCostUSD
	usdToINR := GetUSDToINRRate()
	costSavedINR := costSavedUSD * usdToINR

	return map[string]any{
		"total_messages": totalMessages,
		"total_tokens":   totalTokens,
		"tier_breakdown": map[string]any{
			"free": map[string]any{
				"messages":       freeMessages,
				"tokens":         freeTokens,
				"cost_saved_usd": fmt.Sprintf("$%.4f", freeCostTokensUSD),
			},
			"subscription": map[string]any{
				"messages":       subMessages,
				"tokens":         subTokens,
				"cost_saved_usd": fmt.Sprintf("$%.4f", subCostTokensUSD),
			},
		},
		"model_breakdown":          modelStats,
		"total_media":              totalMedia,
		"total_images":             totalImages,
		"total_videos":             totalVideos,
		"total_music":              totalMusic,
		"total_requests":           totalRequests,
		"cost_saved_usd":           costSavedUSD,
		"cost_saved_inr":           costSavedINR,
		"cost_saved_usd_formatted": fmt.Sprintf("$%.4f", costSavedUSD),
		"cost_saved_inr_formatted": fmt.Sprintf("₹%.2f", costSavedINR),
		"engine":                   "SQLite WAL Mode",
		"database_file":            DBFileName,
	}, nil
}

// RecordAccountUsage inserts or updates account status, tier and usage metrics in SQLite
func RecordAccountUsage(accountID, cookieFile, status, tier string) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()

	if tier == "" {
		tier = "free"
	}

	query := `
	INSERT INTO accounts (account_id, cookie_file, status, tier, total_requests, last_used_at)
	VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
	ON CONFLICT(account_id) DO UPDATE SET
		cookie_file = excluded.cookie_file,
		status = excluded.status,
		tier = excluded.tier,
		total_requests = accounts.total_requests + 1,
		last_used_at = CURRENT_TIMESTAMP;
	`
	_, err := DB.Exec(query, accountID, cookieFile, status, tier)
	return err
}

// GetAccounts retrieves all tracked accounts, tiers, and their usage statistics
func GetAccounts() ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT account_id, cookie_file, status, COALESCE(tier, 'free'), total_requests, last_used_at FROM accounts ORDER BY last_used_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []map[string]any
	for rows.Next() {
		var accountID, cookieFile, status, tier, lastUsed string
		var totalRequests int
		if err := rows.Scan(&accountID, &cookieFile, &status, &tier, &totalRequests, &lastUsed); err == nil {
			accounts = append(accounts, map[string]any{
				"account_id":     accountID,
				"cookie_file":    cookieFile,
				"status":         status,
				"tier":           tier,
				"total_requests": totalRequests,
				"last_used_at":   lastUsed,
			})
		}
	}
	return accounts, nil
}

// GetAllMessagesForExport fetches messages for Excel and JSON export in chronological order (User first, Assistant next)
func GetAllMessagesForExport() ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	query := `SELECT id, conversation_id, user_id, role, content, model, tokens, COALESCE(account_tier, 'free'), COALESCE(account_id, ''), created_at FROM messages ORDER BY id ASC LIMIT 5000`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []map[string]any
	for rows.Next() {
		var id, tokens int
		var convID, userID, role, content, model, tier, accID, createdAt string
		if err := rows.Scan(&id, &convID, &userID, &role, &content, &model, &tokens, &tier, &accID, &createdAt); err == nil {
			list = append(list, map[string]any{
				"id":              id,
				"conversation_id": convID,
				"user_id":         userID,
				"role":            role,
				"content":         content,
				"model":           model,
				"tokens":          tokens,
				"account_tier":    tier,
				"account_id":      accID,
				"created_at":      createdAt,
			})
		}
	}
	return list, nil
}

// GetAllMediaForExport fetches media generations for Excel report generation
func GetAllMediaForExport() ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	query := `SELECT id, type, prompt, file_name, file_path, url, aspect_ratio, response_id, created_at FROM media_generations ORDER BY id DESC LIMIT 5000`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []map[string]any
	for rows.Next() {
		var id int
		var mType, prompt, fileName, filePath, urlStr, aspect, respID, createdAt string
		if err := rows.Scan(&id, &mType, &prompt, &fileName, &filePath, &urlStr, &aspect, &respID, &createdAt); err == nil {
			list = append(list, map[string]any{
				"id":           id,
				"type":         mType,
				"prompt":       prompt,
				"file_name":    fileName,
				"file_path":    filePath,
				"url":          urlStr,
				"aspect_ratio": aspect,
				"response_id":  respID,
				"created_at":   createdAt,
			})
		}
	}
	return list, nil
}

// GetAllRequestLogsForExport fetches request logs for Excel report generation
func GetAllRequestLogsForExport() ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	query := `SELECT id, endpoint, method, user_ip, status_code, elapsed_ms, created_at FROM request_logs ORDER BY id DESC LIMIT 5000`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []map[string]any
	for rows.Next() {
		var id, status int
		var endpoint, method, userIP, createdAt string
		var elapsed float64
		if err := rows.Scan(&id, &endpoint, &method, &userIP, &status, &elapsed, &createdAt); err == nil {
			list = append(list, map[string]any{
				"id":          id,
				"endpoint":    endpoint,
				"method":      method,
				"user_ip":     userIP,
				"status_code": status,
				"elapsed_ms":  elapsed,
				"created_at":  createdAt,
			})
		}
	}
	return list, nil
}

// GetUSDToINRRate returns the exchange rate (default: 95.89 INR/USD, configurable via USD_TO_INR env var)
func GetUSDToINRRate() float64 {
	rate := 95.89
	if envRate := os.Getenv("USD_TO_INR"); envRate != "" {
		if r, err := strconv.ParseFloat(envRate, 64); err == nil && r > 0 {
			rate = r
		}
	}
	return rate
}
