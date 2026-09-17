/**
 * Free Gemini API Sync — Ultra-Lightweight Background Service Worker
 * Pure memory-based zero-tab cookie synchronizer.
 * NEVER opens tabs, NEVER reloads tabs, NEVER touches window focus.
 */

// Candidate server endpoints to auto-discover (Localhost first, then Mac server LAN / ZeroTier / Cluster)
const CANDIDATE_SERVERS = [
  '127.0.0.1',      // 1. Same computer (if running on main server)
  '192.168.1.34',  // 2. Main Mac server (Wi-Fi LAN)
  '10.56.65.136',  // 3. Main Mac server (ZeroTier VPN)
  '192.168.1.9',   // 4. K3s Cluster Master (LAN)
  '10.56.65.169'   // 5. K3s Cluster Master (ZeroTier)
];

let activeHost = CANDIDATE_SERVERS[0];
let currentCandidateIndex = 0;
let ws = null;
let lastSyncTime = null;
let hasSyncedOnce = false;
let syncDebounceTimeout = null;

chrome.runtime.onInstalled.addListener(init);
chrome.runtime.onStartup.addListener(init);

chrome.alarms.onAlarm.addListener(async (alarm) => {
  if (alarm.name === 'reconnect') connectToBackend();
  if (alarm.name === 'keepAlive') keepAlive();
});

async function getServerHost() {
  return activeHost;
}

async function init() {
  const data = await chrome.storage.local.get(['lastWorkingHost', 'lastSyncTime']);
  if (data.lastWorkingHost && CANDIDATE_SERVERS.includes(data.lastWorkingHost)) {
    currentCandidateIndex = CANDIDATE_SERVERS.indexOf(data.lastWorkingHost);
    activeHost = data.lastWorkingHost;
  }
  if (data.lastSyncTime) lastSyncTime = data.lastSyncTime;

  connectToBackend();
  // Lightweight keep-alive ping every 25 seconds to preserve WebSocket channel
  chrome.alarms.create('keepAlive', { periodInMinutes: 0.4 });
}

async function connectToBackend() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const host = CANDIDATE_SERVERS[currentCandidateIndex];
  const wsUrl = `ws://${host}:9226`;
  console.log(`[Gemini Sync] Auto-discovery probing backend at: ${wsUrl}`);
  hasSyncedOnce = false;

  try {
    ws = new WebSocket(wsUrl);
  } catch (e) {
    scheduleNextCandidate();
    return;
  }

  ws.onopen = () => {
    activeHost = host;
    console.log(`[Gemini Sync] Successfully connected to Go Backend on ${host} (Zero-Tab Mode)`);
    chrome.alarms.clear('reconnect');
    chrome.storage.local.set({ lastWorkingHost: host });
    // Read and push cookies instantly from memory on connect
    performSync();
  };

  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.type === 'trigger_sync') {
        console.log('[Gemini Sync] Backend requested fresh cookies. Syncing directly from memory (0 tabs)...');
        performSync();
      }
    } catch (e) {
      console.error(e);
    }
  };

  ws.onclose = () => {
    console.log(`[Gemini Sync] Connection to ${host} closed. Rotating candidate...`);
    scheduleNextCandidate();
  };

  ws.onerror = (err) => {
    console.warn(`[Gemini Sync] Server probe failed for ${host}:9226`);
    try { ws.close(); } catch (_) {}
  };
}

function scheduleNextCandidate() {
  currentCandidateIndex = (currentCandidateIndex + 1) % CANDIDATE_SERVERS.length;
  // If cycling back to start, pause 3s; otherwise probe next host quickly in 500ms
  const delaySec = currentCandidateIndex === 0 ? 3 : 0.5;
  chrome.alarms.create('reconnect', { delayInMinutes: delaySec / 60 });
}

function keepAlive() {
  if (ws?.readyState === WebSocket.OPEN) {
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
  '__Secure-OSID'
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
  const host = await getServerHost();
  getFormattedCookies((formatted) => {
    // If WebSocket is ready, push via WebSocket
    if (ws && ws.readyState === WebSocket.OPEN) {
      console.log(`[Gemini Sync] Synchronized ${formatted.length} essential cookies via WebSocket to ${host}`);
      ws.send(JSON.stringify({
        type: 'cookies_payload',
        cookies: formatted
      }));
    } else {
      // Dual resilience: push via direct HTTP fallback to configured server
      fetch(`http://${host}:8001/api/sync-cookies`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ cookies: formatted })
      }).then(() => {
        console.log(`[Gemini Sync] Synchronized ${formatted.length} cookies via HTTP Fallback to http://${host}:8001`);
      }).catch((e) => {
        console.warn(`[Gemini Sync] HTTP Fallback failed to ${host}:`, e);
      });
      
      connectToBackend();
    }

    hasSyncedOnce = true;
    lastSyncTime = Date.now();
    chrome.storage.local.set({ lastSyncTime });
    chrome.runtime.sendMessage({ type: 'SYNC_UPDATE', success: true, count: formatted.length }).catch(() => {});
  });
}

// ─── Real-Time Passive Cookie Listener ──────────────────────────────────────
// Auto-detect when Google rotates session cookies and push them instantly (zero CPU idle)
chrome.cookies.onChanged.addListener((changeInfo) => {
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
        connected: ws && ws.readyState === WebSocket.OPEN,
        lastSyncTime,
        hasSyncedOnce,
        serverHost: host
      });
    });
    return true; // async sendResponse
  } else if (msg.type === 'SET_SERVER_HOST') {
    const newHost = (msg.host || '').trim() || CANDIDATE_SERVERS[0];
    activeHost = newHost;
    chrome.storage.local.set({ lastWorkingHost: newHost }).then(() => {
      if (ws) {
        try { ws.close(); } catch(e) {}
        ws = null;
      }
      connectToBackend();
      sendResponse({ ok: true, host: newHost });
    });
    return true; // async sendResponse
  } else if (msg.type === 'FORCE_SYNC') {
    performSync();
    sendResponse({ ok: true });
  } else if (msg.type === 'GET_FORMATTED_COOKIES') {
    getFormattedCookies((cookies) => {
      sendResponse({ ok: true, cookies });
    });
    return true; // Keep message channel open for async response
  }
  return true;
});
