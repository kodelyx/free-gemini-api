# 🧩 Gemini Api Agent (Chrome Extension)

A lightweight, automated Chrome Extension (Manifest V3) that syncs active Google Gemini session
cookies to **your own** Free Gemini API Server over a token-authenticated WebSocket bridge.

---

## ⚠️ Read this first

This extension reads your live Google authentication cookies (`SID`, `SAPISID`, `__Secure-1PSID`, …)
and sends them to a server. Those cookies are the account: whoever holds them can act as you without
a password or 2FA prompt.

Therefore:

- **Only pair it with a server running on your own machine, or one you operate.** The host is a value
  you type in yourself; the extension will not go looking for servers.
- **Keep the bridge token private.** It is the only thing standing between your cookies and anything
  else that can reach the port. Don't paste it into issues, screenshots, or chat.
- Recommend a dedicated Google account for this, not your primary one.
- Using an authenticated session against Google's web interface may violate Google's Terms of Service
  and can get an account rate-limited or restricted.

---

## 🚀 Quick Setup & Installation

1. **Clone this repository:**
   ```bash
   git clone https://github.com/kodelyx/free-gemini-api.git
   ```
2. Open Google Chrome and navigate to:
   ```text
   chrome://extensions/
   ```
3. Enable **Developer mode** (toggle switch in the top right corner).
4. Click **Load unpacked** and select the `gemini-extension` folder.
5. **Start your API server and copy the bridge token it prints at startup:**
   ```text
   🔐 Pair the Chrome extension with it: 3f9c…(64 hex chars)
   ```
   The same token lives in `cookies/.bridge_token` on the server.
6. Click the extension icon, paste the token into **Bridge token**, and press **Save Connection**.
   Leave the host at `127.0.0.1` unless the server runs on another machine you control — in that case
   enter its address and tick *allow non-local host*.
7. Log in to [gemini.google.com](https://gemini.google.com). The extension now streams session
   cookies to your server in real time and shows **Paired** in the popup.

Without a token the popup shows **Token required** and the worker transmits nothing.

---

## ⚡ Features

- **Multi-Account Support:** Automatically detects and manages multiple logged-in Google accounts.
- **WebSocket Real-Time Sync:** Instant zero-delay cookie sync on login, rotation, or session refresh.
- **Background Keepalive:** Runs seamlessly in the background without slowing down your browser.
- **One-Time Pairing:** Paste the server's token once; it is kept in `chrome.storage.local`.

---

## 🔒 Network behaviour (what changed after issue #1)

The bridge used to be reachable by anyone who could answer a probe: the extension probed hardcoded
LAN addresses and swept `192.168.1.1-254` for a host returning `{"status":"online"}` on
`:8001/health`, then pushed the cookies to it unencrypted and unauthenticated. On shared Wi-Fi
(campus, café, hotel) that handed a full Google session to whoever ran a listener.

Now:

| Behaviour | Before | After |
| --- | --- | --- |
| Hosts probed | 14 hardcoded LAN IPs + full `192.168.1.0/24` sweep | `127.0.0.1` / `localhost` only, plus a host you configure |
| Authentication | none | server-issued shared secret on every sync |
| Host permissions | `http://*/*`, `https://*/*` always granted | loopback + Google domains; other hosts are an opt-in runtime permission |
| Server identity | trusted any `{"status":"online"}` | HMAC challenge-response: the token is never sent to a host that has not proven it holds it |

---

## 🔗 Related Repositories

- 🚀 **[Free Gemini API Server (Go + Needle 2 SLM + SQLite)](https://github.com/kodelyx/free-gemini-api)**
