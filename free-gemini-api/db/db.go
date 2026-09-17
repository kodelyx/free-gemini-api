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
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
		CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id);

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
			total_requests INTEGER DEFAULT 0,
			last_used_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		`

		if _, err := database.Exec(schema); err != nil {
			initErr = err
			return
		}

		DB = database
		log.Println("🗄️  SQLite Database initialized successfully at data/gemini.db (WAL Mode)")
	})

	return DB, initErr
}

// LogMessage saves a chat turn into SQLite
func LogMessage(conversationID, userID, role, content, model string, tokens int) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	query := `INSERT INTO messages (conversation_id, user_id, role, content, model, tokens) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := DB.Exec(query, conversationID, userID, role, content, model, tokens)
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

// GetSystemStats returns aggregate statistics and official API cost savings from SQLite
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

	// Official Google Cloud / Vertex AI & Gemini API pricing:
	// - Gemini Flash Text: $0.50 / 1M tokens ($0.0000005 per token)
	// - Imagen 3: $0.030 per image
	// - Video Generation: $1.20 per video
	// - Music Generation: $0.08 per audio track
	costSavedUSD := (float64(totalTokens) * 0.0000005) +
		(float64(totalImages) * 0.030) +
		(float64(totalVideos) * 1.200) +
		(float64(totalMusic) * 0.080)
	usdToINR := GetUSDToINRRate()
	costSavedINR := costSavedUSD * usdToINR

	return map[string]any{
		"total_messages":          totalMessages,
		"total_tokens":            totalTokens,
		"total_media":             totalMedia,
		"total_images":            totalImages,
		"total_videos":            totalVideos,
		"total_music":             totalMusic,
		"total_requests":          totalRequests,
		"cost_saved_usd":          costSavedUSD,
		"cost_saved_inr":          costSavedINR,
		"cost_saved_usd_formatted": fmt.Sprintf("$%.4f", costSavedUSD),
		"cost_saved_inr_formatted": fmt.Sprintf("₹%.2f", costSavedINR),
		"engine":                  "SQLite WAL Mode",
		"database_file":           DBFileName,
	}, nil
}

// RecordAccountUsage inserts or updates account status and usage metrics in SQLite
func RecordAccountUsage(accountID, cookieFile, status string) error {
	if DB == nil {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()

	query := `
	INSERT INTO accounts (account_id, cookie_file, status, total_requests, last_used_at)
	VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
	ON CONFLICT(account_id) DO UPDATE SET
		cookie_file = excluded.cookie_file,
		status = excluded.status,
		total_requests = accounts.total_requests + 1,
		last_used_at = CURRENT_TIMESTAMP;
	`
	_, err := DB.Exec(query, accountID, cookieFile, status)
	return err
}

// GetAccounts retrieves all tracked accounts and their usage statistics
func GetAccounts() ([]map[string]any, error) {
	if DB == nil {
		return nil, nil
	}
	rows, err := DB.Query(`SELECT account_id, cookie_file, status, total_requests, last_used_at FROM accounts ORDER BY last_used_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []map[string]any
	for rows.Next() {
		var accountID, cookieFile, status, lastUsed string
		var totalRequests int
		if err := rows.Scan(&accountID, &cookieFile, &status, &totalRequests, &lastUsed); err == nil {
			accounts = append(accounts, map[string]any{
				"account_id":     accountID,
				"cookie_file":    cookieFile,
				"status":         status,
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
	query := `SELECT id, conversation_id, user_id, role, content, model, tokens, created_at FROM messages ORDER BY id ASC LIMIT 5000`
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []map[string]any
	for rows.Next() {
		var id, tokens int
		var convID, userID, role, content, model, createdAt string
		if err := rows.Scan(&id, &convID, &userID, &role, &content, &model, &tokens, &createdAt); err == nil {
			list = append(list, map[string]any{
				"id":              id,
				"conversation_id": convID,
				"user_id":         userID,
				"role":            role,
				"content":         content,
				"model":           model,
				"tokens":          tokens,
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
