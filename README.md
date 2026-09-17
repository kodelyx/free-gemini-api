# 🔓 Free Gemini API Suite

A complete, high-performance toolkit to use **Google Gemini 3.8 Flash**, **Imagen 3 (8K Images)**, **Cinematic Videos**, and **Music Synthesis** for FREE without API keys or costs.

---

## 📂 Repository Structure

| Directory | Description |
|---|---|
| **[`free-gemini-api/`](./free-gemini-api)** | 🚀 Go API Server with Needle 2 SLM Tool Routing & SQLite Memory |
| **[`gemini-extension/`](./gemini-extension)** | 🧩 Chrome Extension for automatic real-time session cookie synchronization |

---

## 🚀 Quick Setup (2 Minutes)

### Step 1: Load the Chrome Extension
1. Open Google Chrome and go to `chrome://extensions/`
2. Enable **Developer mode** (toggle in top right corner).
3. Click **Load unpacked** and select the [`gemini-extension`](./gemini-extension) folder.
4. Log into [gemini.google.com](https://gemini.google.com) — cookies sync automatically to the API server in real-time.

### Step 2: Start the Go API Server
```bash
cd free-gemini-api
go run main.go
```
* **API Server:** `http://localhost:8001`
* **OpenAI Compatible Endpoint:** `http://localhost:8001/v1/chat/completions`

---

## 🧪 Testing the API

```bash
curl -X POST http://localhost:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "messages": [{"role": "user", "content": "Explain quantum computing in 1 sentence"}]
  }'
```
