# 🔓 Free Gemini API Suite

> **Zero cost. Zero API keys. Full power.**
> Use **Google Gemini 3.8 Flash**, **Imagen 3 (8K Images)**, **Cinematic Videos**, and **Music Synthesis** for FREE — with full OpenAI API compatibility.

---

## 📂 Repository Structure

| Directory | Description |
|---|---|
| **[`free-gemini-api/`](./free-gemini-api)** | 🚀 Go API Server — HTTP/3 QUIC + Needle 2 SLM Tool Routing + SQLite Memory |
| **[`gemini-extension/`](./gemini-extension)** | 🧩 Chrome Extension — Zero-tab real-time cookie sync + Copy Cookies button |

---

## ✨ What's New — v2.0 (Latest)

### 🐳 Docker / OrbStack Support
- **Fully self-contained Docker image** — no host folder dependencies
- Multi-stage Alpine build (~25MB final image)
- `restart: unless-stopped` — survives Mac reboots automatically
- Ports `8001` (API) and `9226` (WebSocket bridge) exposed

### 🔄 Reactive Cookie Architecture (Only-On-Expiry)
- **Zero proactive syncs** — cookies are refreshed only when Google returns `401 Unauthorized`
- `ExecuteWithFailover()` auto-detects session expiry and triggers on-demand re-sync
- Passive 30-minute health logger (zero CPU idle impact)
- Dual resilience: **WebSocket** primary + **HTTP POST fallback** (`/api/sync-cookies`)

### 🧩 Chrome Extension Enhancements
- **📋 Copy Cookies button** — one-click clipboard export of all 14 Google session cookies
- `✅ Copied (14 Cookies)!` animated feedback
- Host permissions for `127.0.0.1` and `localhost` (Docker-compatible)
- Real-time passive cookie listener — auto-pushes when Google rotates session tokens

### ⚡ Performance & Stability
- QUIC client timeout reduced `60s → 10s` for fast connection fallback
- Removed duplicate `Cookie` header in HTTP/2 requests (fixed `401` on `tls-client` CookieJar conflict)
- CORS middleware added — extension `fetch()` requests work from all origins
- BPE tokenizer (cl100k_base) for accurate token counting

---

## 🚀 Quick Setup

### Option A — Docker / OrbStack (Recommended)

```bash
# 1. Clone the repo
git clone https://github.com/kodelyx/free-gemini-api.git
cd free-gemini-api/free-gemini-api

# 2. Build and start (fully self-contained)
docker compose up -d --build
```

> The container is **100% independent** — all cookies, database, and analytics are stored inside OrbStack. No host folder mounts needed.

### Option B — Run Natively (Go)

```bash
cd free-gemini-api
go run main.go
```

---

## 🧩 Chrome Extension Setup

1. Open Chrome → `chrome://extensions/`
2. Enable **Developer Mode** (top right toggle)
3. Click **Load unpacked** → select the [`gemini-extension/`](./gemini-extension) folder
4. Log into [gemini.google.com](https://gemini.google.com)
5. Extension auto-connects and pushes cookies to the server via WebSocket

> **Docker users**: Extension connects to `ws://127.0.0.1:9226` — OrbStack routes this seamlessly to the container.

---

## 🌐 API Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/health` | Server status + cookie health |
| `GET` | `/v1/models` | List available models |
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat (streaming supported) |
| `POST` | `/chat` | Unified chat (text, images, video, audio) |
| `POST` | `/music` | Gemini Music generation |
| `POST` | `/api/sync-cookies` | Manual cookie sync (HTTP fallback) |
| `GET` | `/stats` | SQLite analytics + session stats |
| `GET` | `/history` | Conversation history |

---

## 🧪 Test the API

```bash
curl -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "messages": [{"role": "user", "content": "Explain quantum computing in 1 sentence"}]
  }'
```

---

## 🏗️ Architecture

```
Chrome Browser                   Docker Container (OrbStack)
┌──────────────────┐             ┌────────────────────────────┐
│  gemini-extension│──WebSocket──▶ :9226 Cookie Bridge        │
│  (background.js) │──HTTP POST──▶ :8001/api/sync-cookies     │
└──────────────────┘             │                            │
                                 │  Go Fiber HTTP/3 Server    │
Your App / OpenAI Client         │  :8001                     │
┌──────────────────┐             │  ┌──────────────────────┐  │
│  API Requests    │─────────────▶  │ ExecuteWithFailover  │  │
└──────────────────┘             │  │ (401 → auto re-sync) │  │
                                 │  └──────────────────────┘  │
                                 │  SQLite DB  │  Analytics    │
                                 └────────────────────────────┘
```

---

## 📦 Cookie Lifetime (Tested)

| Metric | Result |
|---|---|
| Unbroken session duration | **1h 24m 30s** (76 consecutive requests, 100% pass) |
| Total lifespan tested | **2h 05m 47s** (99 queries) |
| Expiry signal | `HTTP 401 Unauthorized` on StreamGenerate |
| Recovery | Automatic — extension re-syncs fresh cookies instantly |

---

## 🔐 Security Notes

- **Cookies are never committed to git** — `.gitignore` excludes all `cookies/*.json`
- Cookies live only in Chrome memory + server RAM/container filesystem
- No API keys, no billing, no rate limits from Google's side

---

## 📋 Changelog

### v2.0.0 — Docker + Reactive Architecture
- Docker/OrbStack containerization (fully self-contained, no host volume mounts)
- Reactive-only cookie sync — triggers only on `401 Unauthorized`
- CORS middleware + host permissions fix for Docker compatibility
- Copy Cookies button in Chrome extension popup
- BPE tokenizer (cl100k_base), dual analytics export (Excel + JSON)
- `POST /api/sync-cookies` HTTP fallback endpoint

### v1.0.0 — Initial Release
- Go HTTP/3 QUIC server with Gemini 3.7/3.8 Flash
- Chrome Extension with WebSocket cookie bridge
- Needle 2 SLM for native tool calling
- SQLite persistence + conversation history
