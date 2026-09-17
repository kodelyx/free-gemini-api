-- ============================================================================
-- Free Gemini API (pure Go + HTTP/3 QUIC + Gemini 3.8 + Needle 2)
-- High-Performance SQLite Database Schema (data/gemini.db)
-- ============================================================================

-- Concurrency & Ultra-Fast WAL Pragmas
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA temp_store = MEMORY;

-- ----------------------------------------------------------------------------
-- Table: messages
-- Stores chat history across all conversation turns and users
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS messages (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id     TEXT,                                  -- Gemini conversation thread ID (c_...)
    user_id             TEXT,                                  -- Client or IP identifier
    role                TEXT NOT NULL,                         -- 'user' or 'assistant'
    content             TEXT NOT NULL,                         -- Text content of message turn
    model               TEXT,                                  -- Model used (e.g. 'gemini-3.8-flash')
    tokens              INTEGER DEFAULT 0,                     -- Approximate token count
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at);

-- ----------------------------------------------------------------------------
-- Table: media_generations
-- Catalogs all multimodal AI assets generated (Images, Videos, Music)
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS media_generations (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    type                TEXT NOT NULL,                         -- 'image', 'video', or 'music'
    prompt              TEXT NOT NULL,                         -- Generation prompt
    file_name           TEXT NOT NULL,                         -- Saved file name in output/
    file_path           TEXT NOT NULL,                         -- Relative path to saved asset
    url                 TEXT,                                  -- Upstream Google CDN URL
    aspect_ratio        TEXT,                                  -- Aspect ratio (e.g., '16:9', '1:1')
    response_id         TEXT,                                  -- Gemini response identifier
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_media_type ON media_generations(type);
CREATE INDEX IF NOT EXISTS idx_media_created ON media_generations(created_at);

-- ----------------------------------------------------------------------------
-- Table: request_logs
-- Tracks all HTTP API inbound traffic, latencies, and status codes
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS request_logs (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint            TEXT NOT NULL,                         -- e.g. '/chat', '/v1/chat/completions'
    method              TEXT NOT NULL,                         -- GET, POST, etc.
    user_ip             TEXT,                                  -- Remote client IP
    status_code         INTEGER,                               -- HTTP response code
    elapsed_ms          REAL,                                  -- Total turnaround time in ms
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reqlogs_created ON request_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_reqlogs_status ON request_logs(status_code);

-- ----------------------------------------------------------------------------
-- Table: accounts
-- Tracks active cookie worker accounts in the failover pool
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS accounts (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id          TEXT UNIQUE NOT NULL,                  -- Account alias or hash
    cookie_file         TEXT NOT NULL,                         -- Path to cookie file
    status              TEXT DEFAULT 'active',                 -- 'active' or 'error'
    total_requests      INTEGER DEFAULT 0,                     -- Total requests served
    last_used_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts(status);
