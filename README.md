# 🚀 Gemini 4 Argon — Unlimited Free API

<div align="center">

[![Frontier Model](https://img.shields.io/badge/Frontier_Model-Gemini_4_Argon-8A2BE2?style=for-the-badge&logo=google)](https://github.com/kodelyx/free-gemini-api)
[![Thinking Depth](https://img.shields.io/badge/Deep_Thinking-High_%7C_Medium_%7C_Low-00E5FF?style=for-the-badge)](https://github.com/kodelyx/free-gemini-api)
[![Pricing](https://img.shields.io/badge/Cost-100%25_FREE_Unlimited-00C853?style=for-the-badge)](https://github.com/kodelyx/free-gemini-api)
[![OpenAI Compatible](https://img.shields.io/badge/OpenAI_Standard-/v1/chat/completions-FF6F00?style=for-the-badge&logo=openai)](https://github.com/kodelyx/free-gemini-api)
[![Transport](https://img.shields.io/badge/Transport-HTTP%2F3_QUIC_%2B_Pure_Go-007ACC?style=for-the-badge&logo=go)](https://github.com/kodelyx/free-gemini-api)

<p align="center">
  <b>The first open-source, OpenAI-compatible proxy engine that unlocks Google's next-generation <ins>Gemini 4 Argon</ins> frontier model with customizable Thinking Levels (<kbd>High</kbd>, <kbd>Medium</kbd>, <kbd>Low</kbd>) — 100% Free & Unlimited.</b>
</p>

</div>

---

## 🌟 Why Gemini 4 Argon?

**Gemini 4 Argon** is Google's newest, highly-advanced frontier intelligence model engineered specifically for complex tasks, deep logic deduction, and emergent reasoning.

Until now, Argon's thinking controls were locked behind Google's internal web interface. This repository reverse-engineers Google's proprietary **JSPB extension protocol** to expose full programmatic control over Gemini 4 Argon via standard **OpenAI-compatible REST and streaming endpoints**:

- 🧠 **Argon High (`argon-high` / Level 2)**: Deep multi-pass thinking trace for hard logic riddles, competitive programming, and complex architecture.
- ⚖️ **Argon Medium (`argon-medium` / Level 4)**: Balanced thinking depth for structured debugging and comprehensive analysis.
- ⚡ **Argon Low (`argon-low` / Level 1)**: Rapid reasoning with quick self-verification.
- 🚀 **Gemini 3.8 Flash (Default Flagship)**: Powered by `gemini-3.8-flash` as the ultra-fast default engine (`is_default: true`) for everyday zero-latency tasks.

---

## 🔥 Key Highlights

| Feature | Description |
|---|---|
| **🧠 Gemini 4 Argon Native** | Programmatic access to Google's frontier Argon model with multi-level reasoning control (`argon-high`, `argon-medium`, `argon-low`). |
| **⚡ Gemini 3.8 Flash Default** | `gemini-3.8-flash` is set as the default model (`is_default: true`) for instant sub-second completions without configuration. |
| **🔌 Drop-In OpenAI Standard** | Seamlessly connects with **Cursor**, **Cline**, **NextChat**, **OpenWebUI**, **LangChain**, and the standard OpenAI Python / JS SDKs. |
| **🛡️ Distributed Account Pooling** | Connect 1 to 100+ Google accounts to load-balance requests with least-busy routing and automatic rate-limit cooldown management. |
| **🍪 Zero-Touch Cookie Bridge** | Companion Chrome extension syncs session credentials with zero tab manipulation — over a token-authenticated, loopback-by-default bridge. |
| **🚀 Pure Go HTTP/3 QUIC** | State-of-the-art TLS 1.3 fingerprint simulation (Chrome 152 JA4) with 0-RTT session resumption for ultra-low latency. |
| **📊 Dual Intelligence Analytics** | Real-time SQLite WAL storage tracking token usage, latency, and estimated cost-savings exported directly to JSON and Excel (`.xlsx`). |

---

## 📊 Supported Models & Thinking Matrix

| Model ID | Aliases | Thinking Depth | Tier | Role & Description |
|---|---|:---:|:---:|---|
| **`gemini-4-argon`** | `argon`, `4-argon`, `gemini-4` | Low (1) | Free / Auth | 🌟 **Frontier Intelligence** for complex problem solving |
| **`gemini-4-argon-thinking-high`** | `argon-high`, `thinking-high` | **High (2)** | Free / Auth | 🧠 **Deep Reasoning** with maximum thinking steps |
| **`gemini-4-argon-thinking-medium`** | `argon-medium`, `thinking-medium` | **Medium (4)** | Free / Auth | ⚖️ **Balanced Reasoning** for code reviews and analysis |
| **`gemini-4-argon-thinking-low`** | `argon-low`, `thinking-low` | **Low (1)** | Free / Auth | ⚡ **Fast Thinking** with lightweight verification |
| **`gemini-3.8-flash`** | `flash`, `3.8-flash`, `default` | None | Free | 🚀 **Default Model** — Ultra-fast, balanced multimodal flagship |
| **`gemini-3.8-flash-thinking-high`** | `flash-high` | High (2) | Free | ⚡ Flash with deep thinking enabled |
| **`gemini-3.1-pro`** | `pro`, `3.1-pro` | None | Subscription | 💻 Advanced multi-turn coding (Gemini Advanced accounts) |
| **`gemini-3.1-pro-thinking-high`** | `pro-high` | High (2) | Subscription | 🧠 Pro with deep multi-pass reasoning |

---

## ⚡ Quickstart

### 1. Launch the Server

#### Option A: Native Go (Recommended)
```bash
git clone https://github.com/kodelyx/free-gemini-api.git
cd free-gemini-api/free-gemini-api
go run main.go
```

#### Option B: Docker Compose
```bash
cd free-gemini-api/free-gemini-api
docker compose up -d --build
```

The server listens on **`http://127.0.0.1:8001`** (API) and **`ws://127.0.0.1:9226`** (Cookie Bridge).

---

### 2. Connect Your Account (One-Time Pairing)

1. Open `chrome://extensions/` in Chrome.
2. Toggle **Developer mode** on (top-right).
3. Click **Load unpacked** and select the [`gemini-extension/`](./gemini-extension) folder.
4. Copy the bridge token the server printed at startup (also stored in `cookies/.bridge_token`).
5. Click the extension icon, paste the token into **Bridge token**, press **Save Connection**.
6. Log into [gemini.google.com](https://gemini.google.com). The extension streams your session to the
   local API pool and shows **Paired** — until a token is saved it sends nothing.

> The token is the only authentication the cookie bridge has. Treat it like a password: it is what
> stops another device on your network from feeding the pool forged sessions or reading its state.

---

## 💻 Code Examples

### 1. Gemini 4 Argon Deep Thinking (cURL)

```bash
curl -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "argon-high",
    "messages": [
      {
        "role": "user",
        "content": "A farmer has 17 sheep. All but 9 die. How many sheep are left alive? Show your step-by-step thinking."
      }
    ]
  }'
```

### 2. Python (Official OpenAI SDK)

```python
from openai import OpenAI

# Point client to your local Free Gemini 4 Argon server
client = OpenAI(
    base_url="http://localhost:8001/v1",
    api_key="not-needed"
)

# 1. Ask Gemini 4 Argon with High Thinking
response = client.chat.completions.create(
    model="argon-high",
    messages=[
        {"role": "system", "content": "You are a world-class algorithm designer."},
        {"role": "user", "content": "Write a thread-safe LRU cache in Go with O(1) eviction."}
    ]
)
print("🧠 Gemini 4 Argon:", response.choices[0].message.content)

# 2. Use Default Fast Model (Gemini 3.8 Flash)
flash_resp = client.chat.completions.create(
    model="gemini-3.8-flash",
    messages=[{"role": "user", "content": "Explain quantum superposition in 1 sentence."}]
)
print("⚡ Gemini 3.8 Flash:", flash_resp.choices[0].message.content)
```

### 3. JavaScript / TypeScript (Node & Bun)

```typescript
import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "http://localhost:8001/v1",
  apiKey: "free-gemini",
});

async function main() {
  const stream = await openai.chat.completions.create({
    model: "argon-high",
    messages: [{ role: "user", content: "Solve: If 5 machines make 5 widgets in 5 minutes, how long do 100 machines take to make 100 widgets?" }],
    stream: true,
  });

  for await (const chunk of stream) {
    process.stdout.write(chunk.choices[0]?.delta?.content || "");
  }
}

main();
```

---

## 🛠️ Third-Party Client Integrations

| Client / Tool | Base URL | Model Name | API Key |
|---|---|---|---|
| **Cursor / Cline** | `http://localhost:8001/v1` | `argon-high` or `gemini-3.8-flash` | Any string |
| **NextChat / ChatGPT-Next-Web** | `http://localhost:8001/v1` | `argon-high`, `flash-high` | Any string |
| **OpenWebUI / LibreChat** | `http://localhost:8001/v1` | `gemini-4-argon`, `default` | Any string |
| **LangChain / LlamaIndex** | `http://localhost:8001/v1` | `argon-high` | Any string |

---

## 🏛️ System Architecture

```
                 [ Clients / Cursor / OpenAI SDK / Agents ]
                                     │
                                     ▼
                ┌─────────────────────────────────────────┐
                │        Free Gemini API Server           │
                │   (Port 8001: HTTP/3 | Port 9226: WS)   │
                └────────────────────┬────────────────────┘
                                     │
                        ┌────────────┴────────────┐
                        ▼                         ▼
             ┌─────────────────────┐   ┌─────────────────────┐
             │  Model Registry     │   │   Output Arranger   │
             │  - gemini-4-argon   │   │  (Native Tool Calls │
             │  - argon-high       │   │   & Sanitization)   │
             │  - gemini-3.8-flash │   └─────────────────────┘
             └──────────┬──────────┘
                        │
                        ▼
             ┌─────────────────────┐
             │ Multi-Account Pool  │
             │ (Least-Busy Worker) │
             └──────────┬──────────┘
                        │
         ┌──────────────┴──────────────┐
         ▼                             ▼
  ┌──────────────┐              ┌──────────────┐
  │ Google Cloud │              │  Chrome Ext  │
  │ Backend API  │              │ (WebSocket)  │
  └──────────────┘              └──────────────┘
```

---

## 📡 API Reference & Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat & streaming completions (Default: `gemini-3.8-flash`, Frontier: `argon-high`) |
| `GET` | `/v1/models` | List all available models, aliases, and thinking levels |
| `POST` | `/v1/models` | Dynamically register custom model hashes at runtime |
| `POST` | `/v1/models/discover` | Autonomously probe Google backend for new unannounced models |
| `GET` | `/v1/workers` | Real-time worker pool health, active accounts & concurrency metrics |
| `GET` | `/stats` | SQLite token accounting, messages count & financial ROI savings |
| `GET` | `/export/excel` | Download comprehensive multi-sheet Excel report (`.xlsx`) |
| `GET` | `/v1/analytics` | Machine-readable JSON analytics report |
| `GET` | `/health` | Live cookie TTL inspector and cluster health |
| `GET` | `/help` | Terminal developer CLI guide |

---

## ⚙️ CLI Tools

```bash
./goapi --help              # Show terminal CLI guide
./goapi --stats             # Print real-time database usage & cost saved
./goapi --export out.xlsx   # Export detailed spreadsheet analytics
./goapi --mcp               # Run as Model Context Protocol (MCP) server
```

---

## 🔒 Security & Privacy

The companion extension ships **full Google session cookies** — anyone who receives them can
impersonate the signed-in account. The bridge is therefore authenticated, not merely local:

- **Token-Authenticated Bridge (required)**: both cookie-ingest paths — `ws://127.0.0.1:9226` and
  `POST /api/sync-cookies` — require a shared secret. The server generates one on first run,
  stores it in `cookies/.bridge_token` (mode `600`), and prints it once at startup:
  paste it into the extension popup. No token, no cookies accepted.
- **No LAN Sweep in the Extension**: the extension only talks to `127.0.0.1` / `localhost`, or to a
  host you type in yourself (which requires ticking *allow non-local host*). It no longer probes
  hardcoded IPs or sweeps `192.168.1.1-254` for anything answering `/health`, so a device sharing
  your Wi-Fi can no longer harvest a session by answering a probe.
- **Origin-Checked WebSocket**: the bridge rejects handshakes from non-loopback web origins, so a
  page you visit cannot drive the bridge from your own browser.
- **Verified Server Identity**: before it transmits anything the extension sends a random nonce and
  requires an HMAC-SHA256 proof of the bridge token back (`/health?nonce=…`). The token itself is
  never sent to a host that has not proven it holds it, and a proof captured from an earlier probe
  is useless because every probe uses a fresh nonce — a listener that merely answers `/health` gets
  nothing.
- **Minimal Host Permissions**: the extension declares loopback host permissions plus Google
  domains; any other host is an opt-in runtime permission.
- **Locked Down `/health`**: pool internals (account count, cookie TTL, worker count) are only
  returned to clients that present the token.
- **Binds**: `BRIDGE_BIND=127.0.0.1` keeps the bridge off the network entirely.
- **Zero Hardcoded Secrets**: Credentials and cookies are held strictly in local files and memory; nothing is sent to external third-party servers.
- **Safe Version Control**: Pre-configured `.gitignore` prevents private Google session tokens and cookies from ever being committed.

> ⚠️ This project drives Google's web interface with an authenticated session. Doing so may violate
> Google's Terms of Service and can get an account rate-limited or restricted. Use your own accounts,
> and only on a machine and network you control.

---

## 🌟 Support & Community

If you find **Gemini 4 Argon Free API** helpful, please give this repository a ⭐ **Star** to support ongoing development and help other developers discover Google's frontier reasoning models!

---

## 📄 License

MIT License. Developed for the developer and AI research community.
