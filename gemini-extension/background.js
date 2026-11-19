/**
 * Free Gemini API Sync — Ultra-Lightweight Background Service Worker
 * Pure memory-based zero-tab cookie synchronizer.
 * NEVER opens tabs, NEVER reloads tabs, NEVER touches window focus.
 *
 * ── SECURITY MODEL (issue #1) ────────────────────────────────────────────────
 * The cookie payload this worker ships is a full Google session: anyone who
 * receives it can impersonate the signed-in account. Two rules follow from
 * that, and both are enforced below:
 *
 *  1. The extension only ever talks to a host the user explicitly configured,
 *     and only to loopback (127.0.0.1 / localhost) unless the user has ticked
 *     "allow non-local host". There is NO subnet sweep and NO hardcoded IP
 *     list: the old code probed 192.168.1.1-254 for anything answering
 *     {"status":"online"} on :8001/health and then handed it the cookies, so
 *     any device on the same Wi-Fi (campus, cafe, hotel) could harvest a
 *     session by running a five-line listener.
 *
 *  2. Nothing is transmitted until the server proves it holds the same shared
 *     secret, via an HMAC challenge-response (see probeServer) — the secret
 *     itself is never sent to a host that has not been verified, and the
 *     Chromium WebSocket handshake afterwards carries it only to a proven host.
 */

const DEFAULT_HOST = '127.0.0.1';
const API_PORT = 8001;
const WS_PORT = 9226;
const BRIDGE_TOKEN_HEADER = 'X-Bridge-Token';
const LOOPBACK_HOSTS = ['127.0.0.1', 'localhost'];
const MIN_TOKEN_LENGTH = 16;

let config = { host: DEFAULT_HOST, token: '', allowRemoteHost: false };
let activeHost = DEFAULT_HOST;
// The last host that actually proved it holds the shared secret. Cookies are
// only ever written to a host in this position: "answers /health" is not
// identity, a valid HMAC proof is.
let trustedHost = null;
let ws = null;
let wsAuthenticated = false;
let lastSyncTime = null;
let hasSyncedOnce = false;
let lastError = null;
let syncDebounceTimeout = null;
let isDiscovering = false;

chrome.runtime.onInstalled.addListener(init);
chrome.runtime.onStartup.addListener(init);

chrome.alarms.onAlarm.addListener(async (alarm) => {
  if (alarm.name === 'reconnect') connectToBackend();
  if (alarm.name === 'keepAlive') keepAlive();
});

// ─── Host / secret plumbing ──────────────────────────────────────────────────

function isLoopbackHost(host) {
  const h = String(host || '').toLowerCase().replace(/^\[|\]$/g, '');
  return LOOPBACK_HOSTS.includes(h) || h === '::1' || h.startsWith('127.');
}

// Accepts "127.0.0.1", "http://127.0.0.1:8001/path" ... and returns a bare host.
// Ports are not configurable: the API/WS ports are fixed at 8001/9226.
function normalizeHost(raw) {
  let h = String(raw || '').trim().toLowerCase();
  if (!h) return '';
  h = h.replace(/^[a-z][a-z0-9+.-]*:\/\//, ''); // scheme
  h = h.replace(/\/.*$/, '');                   // path
  h = h.replace(/^\[|\]$/g, '');                // ipv6 brackets
  if (!h.startsWith('::')) h = h.replace(/:\d+$/, ''); // port (never for bare ipv6)
  return h.trim();
}

function apiURL(host, path) {
  return `http://${host}:${API_PORT}${path}`;
}

function authHeaders() {
  return config.token ? { [BRIDGE_TOKEN_HEADER]: config.token } : {};
}

function rememberError(msg) {
  lastError = msg || null;
}

// ─── Server discovery (loopback-by-default, configured host on opt-in) ────────

/**
 * Probes one candidate host using an HMAC challenge-response.
 *
 * The secret is deliberately NOT sent on this request: a host that merely
 * answers /health is not yet trusted, and an impostor must not be able to
 * collect the token by squatting on the port. Instead the extension sends a
 * fresh random nonce and the server must return HMAC-SHA256(token, nonce) —
 * something only the real server can compute, and a proof captured earlier is
 * useless because the nonce changes every probe.
 *
 * (Experiment that forced this design: with a plain "send the token and compare
 * a fingerprint" probe, an impostor on 127.0.0.1:8001 that answered
 * {"status":"online"} was handed the full Google session on the HTTP fallback
 * path, and a token-prefixed probe would additionally have leaked the secret.)
 */
async function probeServer(host, timeoutMs = 400) {
  const nonce = randomNonce();
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    const res = await fetch(apiURL(host, `/health?nonce=${nonce}`), { signal: controller.signal });
    clearTimeout(timer);
    if (!res.ok) return null;
    const data = await res.json().catch(() => null);
    if (!data || (data.status !== 'online' && !data.engine)) return null;

    if (!config.token) {
      // Reachable, but nothing has been proven — never a sync target.
      trustedHost = null;
      return host;
    }

    const expected = await hmacProof(config.token, nonce);
    if (!data.bridge_proof || data.bridge_proof !== expected) {
      trustedHost = null;
      rememberError(`Host ${host} answered but could not prove it holds the bridge token`);
      return null;
    }
    rememberError(null);
    trustedHost = host;
    return host;
  } catch (_) {
    return null;
  }
}

function randomNonce() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes).map((b) => b.toString(16).padStart(2, '0')).join('');
}

// HMAC-SHA256(token, nonce) — the same value gemini.BridgeProof returns.
async function hmacProof(token, nonce) {
  const key = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(token),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(nonce));
  return Array.from(new Uint8Array(signature)).map((b) => b.toString(16).padStart(2, '0')).join('');
}

/**
 * Resolve which host to talk to. Loopback candidates are always allowed; a
 * user-configured non-loopback host is only considered when the user has
 * explicitly opted in. Returns null when no candidate could be verified, and
 * callers must treat null as "transmit nothing".
 */
async function discoverServer() {
  if (isDiscovering) return trustedHost;
  isDiscovering = true;

  try {
    const candidates = [config.host, ...LOOPBACK_HOSTS].filter(Boolean);
    const seen = new Set();
    for (const candidate of candidates) {
      const host = normalizeHost(candidate);
      if (!host || seen.has(host)) continue;
      seen.add(host);
      if (!isLoopbackHost(host) && !config.allowRemoteHost) continue;
      const found = await probeServer(host, 400);
      if (found) {
        activeHost = found;
        isDiscovering = false;
        return activeHost;
      }
    }
  } catch (e) {
    console.warn('[Gemini Sync] Auto-discovery error:', e);
  } finally {
    isDiscovering = false;
  }

  trustedHost = null;
  return null;
}

async function getServerHost() {
  return activeHost;
}

async function loadConfig() {
  const data = await chrome.storage.local.get(['bridgeHost', 'bridgeToken', 'allowRemoteHost', 'lastSyncTime']);
  if (data.bridgeHost) config.host = normalizeHost(data.bridgeHost) || DEFAULT_HOST;
  if (typeof data.bridgeToken === 'string') config.token = data.bridgeToken;
  config.allowRemoteHost = data.allowRemoteHost === true;
  if (data.lastSyncTime) lastSyncTime = data.lastSyncTime;

  // Migration: the old build cached an auto-discovered LAN host and re-used it
  // forever. Drop anything that is not loopback unless the user opted in.
  if (!isLoopbackHost(config.host) && !config.allowRemoteHost) {
    config.host = DEFAULT_HOST;
    await chrome.storage.local.set({ bridgeHost: DEFAULT_HOST });
  }
  activeHost = config.host;
  return config;
}

async function saveConfig(next) {
  if (typeof next.host === 'string' && next.host.trim()) {
    config.host = normalizeHost(next.host) || DEFAULT_HOST;
  }
  if (typeof next.token === 'string' && next.token.trim()) {
    config.token = next.token.trim();
  }
  if (typeof next.allowRemoteHost === 'boolean') {
    config.allowRemoteHost = next.allowRemoteHost;
  }
  await chrome.storage.local.set({
    bridgeHost: config.host,
    bridgeToken: config.token,
    allowRemoteHost: config.allowRemoteHost
  });
  activeHost = config.host;
  trustedHost = null; // re-verify against the new host/secret
  return config;
}

async function init() {
  await loadConfig();
  connectToBackend();
  // Lightweight keep-alive ping every 25 seconds to preserve WebSocket channel
  chrome.alarms.create('keepAlive', { periodInMinutes: 0.4 });
}

// ─── Cookie Bridge (WebSocket, secret-authenticated) ─────────────────────────

async function connectToBackend() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  if (!config.token) {
    // No secret -> no cookie leaves the browser. The popup asks the user to
    // pair the extension with their server first.
    rememberError('Bridge token not configured. Paste the token printed by the server.');
    return;
  }

  const host = await discoverServer();
  if (!host) {
    rememberError(`No server at ${config.host} proved it holds the bridge token — no cookies sent.`);
    wsAuthenticated = false;
    notifyPopup();
    scheduleReconnect();
    return;
  }
  wsAuthenticated = false;

  const wsUrl = `ws://${host}:${WS_PORT}`;
  console.log(`[Gemini Sync] Connecting to backend at: ${wsUrl}`);
  hasSyncedOnce = false;

  try {
    ws = new WebSocket(wsUrl);
  } catch (e) {
    scheduleReconnect();
    return;
  }

  ws.onopen = () => {
    // Handshake first: the server closes the socket if the secret is wrong.
    ws.send(JSON.stringify({ type: 'auth', token: config.token }));
  };

  ws.onmessage = (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (e) {
      console.error(e);
      return;
    }

    if (msg.type === 'auth_ok') {
      wsAuthenticated = true;
      rememberError(null);
      console.log(`[Gemini Sync] Authenticated with Go backend on ${host} (Zero-Tab Mode)`);
      chrome.alarms.clear('reconnect');
      performSync();
      return;
    }

    if (msg.type === 'error' && msg.error === 'unauthorized') {
      wsAuthenticated = false;
      rememberError('Server rejected the bridge token');
      console.warn('[Gemini Sync] Server rejected the bridge token — no cookies sent.');
      try { ws.close(); } catch (_) {}
      return;
    }

    if (msg.type === 'trigger_sync' && wsAuthenticated) {
      console.log('[Gemini Sync] Backend requested fresh cookies. Syncing from memory (0 tabs)...');
      performSync();
    }
  };

  ws.onclose = () => {
    console.log(`[Gemini Sync] Connection to ${host} closed. Re-discovering...`);
    wsAuthenticated = false;
    scheduleReconnect();
  };

  ws.onerror = () => {
    console.warn(`[Gemini Sync] WebSocket probe failed for ${host}:${WS_PORT}`);
    try { ws.close(); } catch (_) {}
  };
}

function scheduleReconnect() {
  if (!config.token) return;
  chrome.alarms.create('reconnect', { delayInMinutes: 0.05 }); // ~3s
}

function keepAlive() {
  if (ws?.readyState === WebSocket.OPEN && wsAuthenticated) {
    ws.send(JSON.stringify({ type: 'ping' }));
  } else {
    connectToBackend();
  }
}

const ALLOWED_COOKIE_NAMES = new Set([
  '__Secure-1PSID',
  '__Secure-3PSID',
  '__Secure-1PAPISID',
  '__Secure-3PAPISID',
  '__Secure-1PSIDTS',
  '__Secure-3PSIDTS',
  '__Secure-1PSIDCC',
  '__Secure-3PSIDCC',
  'SID',
  'HSID',
  'SSID',
  'APISID',
  'SAPISID',
  'SIDCC',
  'OSID',
  '__Secure-OSID',
  'COMPASS',
  'NID'
]);

// Extracts and formats essential cookies directly from Chrome memory
function getFormattedCookies(callback) {
  chrome.cookies.getAll({}, (cookies) => {
    const googleCookies = cookies.filter(c =>
      (c.domain === '.google.com' || c.domain === '.google.co' || c.domain === 'gemini.google.com' || c.domain.includes('googleusercontent.com')) &&
      (ALLOWED_COOKIE_NAMES.has(c.name) || c.domain.includes('googleusercontent.com'))
    );

    const formatted = googleCookies.map(c => {
      let exp = c.expirationDate;
      if (!exp || c.session) {
        exp = Math.floor(Date.now() / 1000) + 31536000; // Default 1 year
      }

      let sameSite = c.sameSite || 'unspecified';
      if (sameSite === 'no_restriction') sameSite = 'none';

      return {
        domain: c.domain,
        expirationDate: exp,
        hostOnly: c.hostOnly,
        httpOnly: c.httpOnly,
        name: c.name,
        path: c.path,
        sameSite: sameSite,
        secure: c.secure,
        session: c.session,
        storeId: c.storeId || '0',
        value: c.value
      };
    });

    callback(formatted);
  });
}

// Performs instantaneous memory extraction and WebSocket/HTTP transfer (< 2ms, ZERO tabs)
async function performSync() {
  if (!config.token) {
    rememberError('Bridge token not configured. Paste the token printed by the server.');
    notifyPopup();
    return;
  }

  // The HTTP fallback can only ever target a host that proved it holds the
  // secret. Without this gate an impostor listening on the loopback port was
  // handed the cookies the moment the WebSocket failed.
  if (!wsAuthenticated && activeHost !== trustedHost) {
    await discoverServer();
  }
  if (!wsAuthenticated && activeHost !== trustedHost) {
    rememberError('Refusing to send cookies: no server proved it holds the bridge token.');
    notifyPopup();
    return;
  }

  const host = activeHost;
  getFormattedCookies((formatted) => {
    let sent = false;

    // If the WebSocket is authenticated, push via WebSocket
    if (ws && ws.readyState === WebSocket.OPEN && wsAuthenticated) {
      ws.send(JSON.stringify({
        type: 'cookies_payload',
        token: config.token,
        cookies: formatted
      }));
      sent = true;
      console.log(`[Gemini Sync] Synchronized ${formatted.length} essential cookies via WebSocket to ${host}`);
    } else if (isLoopbackHost(host) || config.allowRemoteHost) {
      // Dual resilience: authenticated HTTP fallback to the verified server
      fetch(apiURL(host, '/api/sync-cookies'), {
        method: 'POST',
        headers: Object.assign({ 'Content-Type': 'application/json' }, authHeaders()),
        body: JSON.stringify({ cookies: formatted })
      }).then((res) => {
        if (!res.ok) {
          rememberError(`Server rejected cookie sync (HTTP ${res.status})`);
        } else {
          sent = true;
          console.log(`[Gemini Sync] Synchronized ${formatted.length} cookies via HTTP fallback to ${apiURL(host, '')}`);
        }
      }).catch((e) => {
        rememberError(`HTTP fallback failed: ${e}`);
        console.warn(`[Gemini Sync] HTTP fallback failed to ${host}:`, e);
      });

      connectToBackend();
    }

    if (sent) {
      hasSyncedOnce = true;
      lastSyncTime = Date.now();
      chrome.storage.local.set({ lastSyncTime });
    }
    notifyPopup();
  });
}

function notifyPopup() {
  chrome.runtime
    .sendMessage({ type: 'SYNC_UPDATE', success: hasSyncedOnce, count: 0, error: lastError })
    .catch(() => {});
}

// ─── Real-Time Passive Cookie Listener ──────────────────────────────────────
// Auto-detect when Google rotates session cookies and push them instantly (zero CPU idle)
chrome.cookies.onChanged.addListener((changeInfo) => {
  if (!config.token) return; // nothing is transmitted without a paired secret

  const cookie = changeInfo.cookie;

  const isTargetDomain = cookie.domain === '.google.com' ||
                         cookie.domain === '.google.co' ||
                         cookie.domain === 'gemini.google.com' ||
                         cookie.domain.includes('googleusercontent.com');

  if (isTargetDomain && ALLOWED_COOKIE_NAMES.has(cookie.name)) {
    if (changeInfo.removed) return;

    // Debounce multiple fast updates (Google rotates 2-3 tokens in a single batch)
    if (syncDebounceTimeout) clearTimeout(syncDebounceTimeout);
    syncDebounceTimeout = setTimeout(() => {
      performSync();
    }, 1500);
  }
});

// Receive message from Popup
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === 'GET_STATUS') {
    getServerHost().then((host) => {
      sendResponse({
        connected: !!(ws && ws.readyState === WebSocket.OPEN && wsAuthenticated),
        verified: wsAuthenticated || activeHost === trustedHost,
        lastSyncTime,
        hasSyncedOnce,
        serverHost: host,
        configuredHost: config.host,
        tokenSet: !!config.token,
        allowRemoteHost: config.allowRemoteHost,
        error: lastError
      });
    });
    return true; // async sendResponse
  } else if (msg.type === 'SET_CONFIG') {
    (async () => {
      const requestedHost = normalizeHost(msg.host) || DEFAULT_HOST;
      const allowRemote = msg.allowRemoteHost === true;

      if (!isLoopbackHost(requestedHost) && !allowRemote) {
        sendResponse({
          ok: false,
          error: 'Refusing a non-local host unless "allow non-local host" is ticked.'
        });
        return;
      }

      if (!isLoopbackHost(requestedHost) && allowRemote) {
        // A user-chosen host needs a runtime-granted host permission.
        const granted = await chrome.permissions.request({ origins: ['http://*/*', 'https://*/*'] }).catch(() => false);
        if (!granted) {
          sendResponse({ ok: false, error: 'Host permission was not granted for that host.' });
          return;
        }
      }

      await saveConfig({ host: requestedHost, token: msg.token || '', allowRemoteHost: allowRemote });
      if (ws) {
        try { ws.close(); } catch (e) {}
        ws = null;
      }
      wsAuthenticated = false;
      trustedHost = null;
      rememberError(null);
      connectToBackend();
      sendResponse({
        ok: true,
        host: config.host,
        tokenSet: !!config.token,
        allowRemoteHost: config.allowRemoteHost
      });
    })();
    return true; // async sendResponse
  } else if (msg.type === 'FORCE_SYNC') {
    (async () => {
      if (!config.token) {
        sendResponse({ ok: false, error: 'Bridge token not configured.' });
        return;
      }
      await performSync();
      sendResponse({ ok: true, error: lastError });
    })();
    return true;
  } else if (msg.type === 'GET_FORMATTED_COOKIES') {
    // Local-only affordance: the user copies their own cookies into their own
    // clipboard. Nothing here transmits.
    getFormattedCookies((cookies) => {
      sendResponse({ ok: true, cookies });
    });
    return true; // Keep message channel open for async response
  }
  return true;
});

// Load config as soon as the worker spins up (MV3 workers are ephemeral).
loadConfig().then(connectToBackend);
