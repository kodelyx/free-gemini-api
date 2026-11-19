# 🧩 Gemini Api Agent (Chrome Extension)

A lightweight, automated Chrome Extension (Manifest V3) that automatically syncs active Google Gemini session cookies to your local **[Free Gemini API Server](https://github.com/kodelyx/free-gemini-api)** via real-time WebSockets.

---

## 🚀 Quick Setup & Installation

1. **Download / Clone this repository:**
   ```bash
   git clone https://github.com/kodelyx/gemini-extension.git
   ```
2. Open Google Chrome and navigate to:
   ```text
   chrome://extensions/
   ```
3. Enable **Developer mode** (toggle switch in the top right corner).
4. Click **Load unpacked** and select the `gemini-extension` folder.
5. Log in to [gemini.google.com](https://gemini.google.com).
6. The extension will automatically detect your account and stream active session cookies to your local API server (`ws://127.0.0.1:9226`) in real-time!

---

## ⚡ Features

- **Multi-Account Support:** Automatically detects and manages multiple logged-in Google accounts.
- **WebSocket Real-Time Sync:** Instant zero-delay cookie sync on login, rotation, or session refresh.
- **Background Keepalive:** Runs seamlessly in the background without slowing down your browser.
- **Zero Configuration:** Works right out of the box with the Free Gemini API Server.

---

## 🔗 Related Repositories

- 🚀 **[Free Gemini API Server (Go + Needle 2 SLM + SQLite)](https://github.com/kodelyx/free-gemini-api)**
- 🐍 **[Gemini Engine Python SDK](https://github.com/kodelyx/free-gemini-api)**
