# 🚀 Gemini 4 Hydrogen & Neon — Unlimited Free OpenAI-Compatible API

<div align="center">

[![Flagship Everyday](https://img.shields.io/badge/Everyday_Help-Gemini_4_Hydrogen-00C853?style=for-the-badge&logo=google)](https://github.com/kodelyx/free-gemini-api)
[![Frontier Reasoning](https://img.shields.io/badge/Deeper_Analysis-Gemini_4_Neon-8A2BE2?style=for-the-badge&logo=google)](https://github.com/kodelyx/free-gemini-api)
[![Thinking Depth](https://img.shields.io/badge/Thinking_Depth-High_%7C_Medium_%7C_Low-00E5FF?style=for-the-badge)](https://github.com/kodelyx/free-gemini-api)
[![Agentic Task Mode](https://img.shields.io/badge/Agentic_Mode-Chat_%26_Task_[40]-FF6F00?style=for-the-badge)](https://github.com/kodelyx/free-gemini-api)
[![Pricing](https://img.shields.io/badge/Cost-100%25_FREE_Unlimited-00E676?style=for-the-badge)](https://github.com/kodelyx/free-gemini-api)
[![Transport](https://img.shields.io/badge/Transport-HTTP%2F3_QUIC_%2B_Pure_Go-007ACC?style=for-the-badge&logo=go)](https://github.com/kodelyx/free-gemini-api)

<p align="center">
  <b>The open-source, drop-in OpenAI-compatible proxy engine that unlocks Google's official <ins>Gemini 4 Hydrogen</ins> (Everyday Workhorse), <ins>Gemini 4 Neon</ins> (Deeper Analysis with High/Medium/Low Thinking Controls), and <ins>Agentic Task Mode</ins> — 100% Free & Unlimited.</b>
</p>

[⭐ Star this repo on GitHub](https://github.com/kodelyx/free-gemini-api) • [⚡ Quickstart](#-quickstart) • [📊 Model Matrix](#-supported-models--thinking-matrix) • [💻 Code Examples](#-code-examples) • [🔌 Integrations](#-third-party-client-integrations)

</div>

---

## 🌟 What's New in Gemini 4?

Google has officially categorized its next-generation Gemini 4 lineup with specialized elemental tiers and an all-new **Agentic Task Mode**:

- 🚀 **Gemini 4 Hydrogen (`gemini-4-hydrogen` / `hydrogen`)**: Google's lightning-fast, high-throughput multimodal flagship (`is_default: true`). Engineered for everyday coding, fast conversations, and zero-latency problem-solving. **100% Free for everyone**.
- 🧠 **Gemini 4 Neon (`gemini-4-neon` / `neon`)**: The heavy-duty analytical engine for deep logic deductions, architecture designs, and multi-turn complex reasoning.
- 🎛️ **Thinking Depth Controls (Neon)**:
  - 🧠 **Neon High (`neon-high` / Level 2)**: Maximum multi-pass reasoning trace for complex math, algorithmic logic, and hard bug investigations.
  - ⚖️ **Neon Medium (`neon-medium` / Level 4)**: Balanced reasoning depth for comprehensive code reviews and analytical summaries.
  - ⚡ **Neon Low (`neon-low` / Level 1)**: Rapid self-verification with minimal overhead.
- 📋 **Chat vs Agentic Task Mode (`[0]` vs `[40]`)**: Toggle between standard reactive conversational Q&A (`[0]`) and Google's autonomous multi-step agentic planner (`[40]`) via `hydrogen-task`, `neon-task`, or header `X-Gemini-Mode: task`.
- 🔄 **Adaptive Auto (`gemini-auto` / `auto`)**: Google's smart auto-mode that dynamically adapts model capacity and reasoning to your query's complexity.
- 🛡️ **Ultra-Ready Architecture**: Background queue ready for Ultra models (**Gemini 4 Argon** & **Gemini 4 Carbon**) whenever an Ultra account is linked.

---

## 🔥 Key Highlights

| Feature | Description |
|---|---|
| **🚀 Gemini 4 Hydrogen Default** | Sub-second zero-latency responses out of the box with zero configuration needed. |
| **🧠 Gemini 4 Neon Deep Thinking** | Programmatic access to Google's frontier reasoning engine with multi-level thinking depth control (`neon-high`, `neon-medium`, `neon-low`). |
| **📋 Native Agentic Task Mode** | Supports Google's new Task mode (`[40]`) for multi-step project planning and autonomous workflows alongside standard Chat (`[0]`). |
| **🔌 Drop-In OpenAI Standard** | Connects seamlessly with **Cursor**, **Cline**, **NextChat**, **OpenWebUI**, **LangChain**, and official OpenAI Python & TypeScript SDKs. |
| **🛡️ Distributed Account Pooling** | Connect 1 to 100+ accounts with automatic least-busy load-balancing, tier segregation (Free / Pro / Ultra), and cooldown protection. |
| **🍪 Zero-Touch Cookie Bridge** | Companion Chrome extension extracts cookies directly from browser memory and syncs via local WebSocket (`ws://127.0.0.1:9226`) with 0 open tabs. |
| **⚡ Pure Go HTTP/3 QUIC** | Native TLS 1.3 fingerprint simulation (Chrome 152 JA4) with 0-RTT session resumption for minimal round-trip latency. |
| **📊 Dual Intelligence Analytics** | Real-time SQLite WAL accounting tracking tokens, latency, and estimated cost savings with direct JSON & Excel (`.xlsx`) export. |

---

## 📊 Supported Models & Thinking Matrix

| Model ID | Aliases | Thinking Depth | Mode | Tier | Role & Description |
|---|---|:---:|:---:|:---:|---|
| **`gemini-4-hydrogen`** | `hydrogen`, `4-hydrogen`, `default` | None | Chat (`[0]`) | **Free** | 🚀 **Everyday Help** — Ultra-fast balanced multimodal flagship (Default) |
| **`gemini-4-hydrogen-task`**| `hydrogen-task`, `4-hydrogen-task` | None | **Task (`[40]`)** | **Free** | 📋 **Task Mode** — Agentic multi-step task execution & planning |
| **`gemini-4-neon`** | `neon`, `4-neon`, `deeper-analysis` | None | Chat (`[0]`) | **Pro** | 💻 **Deeper Analysis** — Complex coding and multi-turn problem solving |
| **`gemini-4-neon-task`** | `neon-task`, `4-neon-task` | None | **Task (`[40]`)** | **Pro** | 📋 **Task Mode** — Deep agentic task execution & complex workflows |
| **`gemini-4-neon-thinking-high`** | `neon-high`, `thinking-high` | **High (2)** | Chat (`[0]`) | **Pro** | 🧠 **Deep Reasoning** with maximum thorough thinking steps |
| **`gemini-4-neon-thinking-medium`** | `neon-medium`, `thinking-medium` | **Medium (4)** | Chat (`[0]`) | **Pro** | ⚖️ **Balanced Reasoning** for code reviews and analysis |
| **`gemini-4-neon-thinking-low`** | `neon-low`, `thinking-low` | **Low (1)** | Chat (`[0]`) | **Pro** | ⚡ **Fast Thinking** with lightweight verification |
| **`gemini-auto`** | `auto`, `gemini-auto` | Auto | Chat (`[0]`) | **Free** | 🔄 **Adaptive Model** — Automatically adapts to your needs |
| **`gemini-4-argon`** | `argon`, `4-argon`, `gemini-4` | Low (1) | Chat (`[0]`) | **Ultra** | 🌟 **Frontier Model** for complex problem solving |
| **`gemini-4-carbon`** | `carbon`, `4-carbon` | High (2) | Chat (`[0]`) | **Ultra** | 🧪 **Long-Horizon Agent** for autonomous workflows |
| **`gemini-3.8-flash`** | `flash`, `3.8-flash` | None | Chat (`[0]`) | **Free** | ⚡ Legacy Flash alias (routes to Hydrogen) |
| **`gemini-3.1-pro`** | `pro`, `3.1-pro` | None | Chat (`[0]`) | **Pro** | 💻 Legacy Pro alias (routes to Neon) |

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

The server listens on:
* **`http://127.0.0.1:8001`** — OpenAI REST & Streaming API
* **`ws://127.0.0.1:9226`** — Zero-Touch Cookie Bridge

---

### 2. Connect Your Account (Zero-Touch)

1. Open `chrome://extensions/` in Chrome.
2. Toggle **Developer mode** on (top-right).
3. Click **Load unpacked** and select the [`gemini-extension/`](./gemini-extension) folder.
4. Log into [gemini.google.com](https://gemini.google.com).
5. The extension detects your active session and streams the worker credentials to the local API pool automatically!

---

## 💻 Code Examples

### 1. Gemini 4 Neon Deep Thinking (Python — Official OpenAI SDK)

```python
from openai import OpenAI

# Point client to your local Free Gemini API server
client = OpenAI(
    base_url="http://localhost:8001/v1",
    api_key="not-needed"
)

# 1. Ask Gemini 4 Neon with High Thinking Depth
response = client.chat.completions.create(
    model="neon-high",
    messages=[
        {"role": "system", "content": "You are a world-class distributed systems engineer."},
        {"role": "user", "content": "Design a high-throughput, fault-tolerant message queue in Go with 0-copy I/O."}
    ]
)
print("🧠 Gemini 4 Neon (High Thinking):", response.choices[0].message.content)

# 2. Use Everyday Fast Model (Gemini 4 Hydrogen)
flash_resp = client.chat.completions.create(
    model="hydrogen",
    messages=[{"role": "user", "content": "Explain quantum entanglement in 1 sentence."}]
)
print("⚡ Gemini 4 Hydrogen:", flash_resp.choices[0].message.content)
```

---

### 2. Real-Time Streaming (Node.js / Bun / TypeScript)

```typescript
import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "http://localhost:8001/v1",
  apiKey: "free-gemini",
});

async function main() {
  const stream = await openai.chat.completions.create({
    model: "neon-medium",
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

### 3. Agentic Task Mode Execution (cURL)

Execute requests in Google's autonomous Task Mode (`[40]`) using either the model alias or header:

```bash
# Using model alias:
curl -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "hydrogen-task",
    "messages": [
      {
        "role": "user",
        "content": "Create a multi-step execution plan for deploying a zero-downtime PostgreSQL cluster."
      }
    ]
  }'

# Or using the X-Gemini-Mode header:
curl -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Gemini-Mode: task" \
  -d '{
    "model": "neon-high",
    "messages": [
      {
        "role": "user",
        "content": "Audit this smart contract architecture and build an automated remediation checklist."
      }
    ]
  }'
```

---

## 🛠️ Third-Party Client Integrations

| Client / Tool | Base URL | Model Name | API Key |
|---|---|---|---|
| **Cursor / Cline** | `http://localhost:8001/v1` | `neon-high`, `neon`, or `hydrogen` | Any string |
| **NextChat / ChatGPT-Next-Web** | `http://localhost:8001/v1` | `neon-high`, `hydrogen` | Any string |
| **OpenWebUI / LibreChat** | `http://localhost:8001/v1` | `gemini-4-neon`, `gemini-4-hydrogen`, `auto` | Any string |
| **LangChain / LlamaIndex** | `http://localhost:8001/v1` | `neon-high`, `hydrogen-task` | Any string |

---

## 🏛️ System Architecture

```
                 [ Cursor / Cline / OpenAI SDK / AI Agents ]
                                      │
                                      ▼
                 ┌─────────────────────────────────────────┐
                 │        Free Gemini API Server           │
                 │   (Port 8001: HTTP/3 | Port 9226: WS)   │
                 └────────────────────┬────────────────────┘
                                      │
     ┌────────────────────────────────┼────────────────────────────────┐
     ▼                                ▼                                ▼
┌─────────────────────┐   ┌─────────────────────┐   ┌─────────────────────┐
│   Model Registry    │   │  Mode Controller    │   │   Output Arranger   │
│ - gemini-4-hydrogen │   │ - Chat Mode   ([0]) │   │  (Native Tool Calls │
│ - gemini-4-neon     │   │ - Task Mode  ([40]) │   │   & Sanitization)   │
│ - neon-high (lvl 2) │   └─────────────────────┘   └─────────────────────┘
│ - gemini-auto       │
└──────────┬──────────┘
           │
           ▼
┌────────────────────────────────────────┐
│     Multi-Account Worker Pool          │
│  - Free Workers    (Hydrogen & Auto)   │
│  - Pro Workers     (Neon & Thinking)   │
│  - Ultra Workers   (Argon & Carbon)    │
└──────────────────┬─────────────────────┘
                   │
    ┌──────────────┴──────────────┐
    ▼                             ▼
┌──────────────┐           ┌──────────────┐
│ Google Cloud │           │  Chrome Ext  │
│ Backend API  │           │ (WebSocket)  │
└──────────────┘           └──────────────┘
```

---

## 📡 API Reference & Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat & streaming completions (Models: `hydrogen`, `neon-high`, `auto`, etc.) |
| `GET` | `/v1/models` | List all dynamic models, aliases, tiers, and thinking levels |
| `POST` | `/v1/models` | Register custom model hashes & mode IDs dynamically at runtime |
| `POST` | `/v1/models/discover` | Autonomously probe Google backend for newly rolled out models |
| `GET` | `/v1/workers` | Real-time worker pool metrics, active account tiers & throughput |
| `GET` | `/stats` | SQLite token accounting, messages count & financial ROI savings |
| `GET` | `/export/excel` | Download comprehensive multi-sheet Excel report (`.xlsx`) |
| `GET` | `/v1/analytics` | Machine-readable JSON analytics report |
| `GET` | `/health` | Live cookie health, TTL inspector & extension status |
| `GET` | `/help` | Terminal developer CLI manual |

---

## ⚙️ CLI Tools

```bash
./goapi                     # Start the API server daemon (Port 8001 & WS 9226)
./goapi --help              # Show terminal CLI developer manual
./goapi --stats             # Print real-time database usage & cost saved
./goapi --export out.xlsx   # Export detailed spreadsheet analytics
./goapi --mcp               # Run as Model Context Protocol (MCP) server
```

---

## 🔒 Security & Privacy

- **Zero Hardcoded Secrets**: Credentials and cookies are stored strictly in local memory and local files; nothing is sent to external third parties.
- **Local Network Isolation**: All WebSocket discovery and cookie sync happen strictly over localhost or your local private LAN.
- **Safe Version Control**: Pre-configured `.gitignore` prevents private Google session tokens and cookies from ever being committed to git.

---

## 🌟 Support & Community

If you find **Free Gemini API** helpful, please give this repository a ⭐ **Star** to support ongoing open-source development and help other developers discover Google's latest Gemini 4 models!

---

## 📄 License

MIT License. Built for the developer, hacker, and AI research community.
