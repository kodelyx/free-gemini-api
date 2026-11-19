package gemini

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ─── Shared-secret gate for the cookie bridge ───────────────────────────────
//
// Reported as issue #1: the Chrome extension swept 192.168.1.1-254 for any host
// answering {"status":"online"} on :8001/health and then pushed the user's
// complete Google session cookies to it over ws://:9226 or
// POST /api/sync-cookies — unauthenticated and unencrypted. Server-side there
// was nothing to stop that: the WS upgrader accepted every Origin, both ingest
// paths took whoever connected first, and the bridge listened on 0.0.0.0.
//
// This file is the server half of the fix. Cookie ingest now requires a shared
// secret that is generated locally on first run (or supplied via BRIDGE_TOKEN),
// so answering a LAN probe with a fake /health earns an attacker nothing: the
// payload is refused before it reaches ProcessAndSaveCookies.
//
// The secret never has to be typed by hand anywhere except the extension popup,
// and a client can verify it is talking to the real server by comparing the
// fingerprint the server echoes for a valid secret.

const (
	// BridgeTokenHeader is the header carrying the shared secret.
	BridgeTokenHeader = "X-Bridge-Token"
	// BridgeTokenQuery is the query parameter alternative (WebSocket clients
	// that cannot set headers).
	BridgeTokenQuery = "token"

	bridgeTokenEnv      = "BRIDGE_TOKEN"
	bridgeTokenDisable  = "BRIDGE_TOKEN_DISABLED"
	bridgeTokenFileName = "cookies/.bridge_token"
	bridgeBindEnv       = "BRIDGE_BIND"

	// BridgeMinTokenLength rejects trivially guessable secrets.
	BridgeMinTokenLength = 16
)

var (
	bridgeTokenOnce sync.Once
	bridgeTokenMu   sync.RWMutex
	bridgeTokenVal  string
	bridgeTokenPath string
)

// BridgeTokenDisabled reports whether the operator explicitly turned the gate
// off with BRIDGE_TOKEN_DISABLED=1. It exists so an existing deployment whose
// other machines cannot be reconfigured yet keeps working while still logging
// the exposure loudly.
func BridgeTokenDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(bridgeTokenDisable))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// InitBridgeToken loads the shared secret, generating one on first run.
// Sources, in order: BRIDGE_TOKEN env, cookies/.bridge_token, a fresh 32-byte
// random value persisted to cookies/.bridge_token with 0600.
func InitBridgeToken() string {
	bridgeTokenOnce.Do(func() {
		if env := strings.TrimSpace(os.Getenv(bridgeTokenEnv)); env != "" {
			if len(env) < BridgeMinTokenLength {
				log.Printf("⚠️  %s is shorter than %d characters and is easy to brute-force", bridgeTokenEnv, BridgeMinTokenLength)
			}
			bridgeTokenMu.Lock()
			bridgeTokenVal = env
			bridgeTokenPath = "(from " + bridgeTokenEnv + ")"
			bridgeTokenMu.Unlock()
			log.Printf("🔐 Cookie bridge secret loaded from %s", bridgeTokenEnv)
			return
		}

		path := bridgeTokenFileName
		if data, err := os.ReadFile(path); err == nil {
			if token := strings.TrimSpace(string(data)); len(token) >= BridgeMinTokenLength {
				bridgeTokenMu.Lock()
				bridgeTokenVal = token
				bridgeTokenPath = path
				bridgeTokenMu.Unlock()
				log.Printf("🔐 Cookie bridge secret loaded from %s", path)
				return
			}
			log.Printf("⚠️  %s is missing or shorter than %d characters; minting a new secret", path, BridgeMinTokenLength)
		}

		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			log.Fatalf("❌ Could not generate a cookie bridge secret: %v", err)
		}
		token := hex.EncodeToString(buf)

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			log.Printf("⚠️  Could not create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
			log.Printf("⚠️  Could not persist the cookie bridge secret to %s: %v (it will change on restart)", path, err)
			path = "(not persisted)"
		}

		bridgeTokenMu.Lock()
		bridgeTokenVal = token
		bridgeTokenPath = path
		bridgeTokenMu.Unlock()

		// Printed once, deliberately: this is the pairing code the user pastes
		// into the extension popup. It is a local secret, never sent anywhere.
		log.Printf("🔐 Generated a new cookie bridge secret (stored in %s)", path)
		log.Printf("🔐 Pair the Chrome extension with it: %s", token)
	})

	// Read directly: calling BridgeToken() here would recurse through
	// InitBridgeToken() and blow the stack.
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	return bridgeTokenVal
}

// BridgeToken returns the active shared secret, initialising it if needed.
func BridgeToken() string {
	InitBridgeToken()
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	return bridgeTokenVal
}

// BridgeTokenSource reports where the secret came from, for startup logging.
func BridgeTokenSource() string {
	InitBridgeToken()
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	return bridgeTokenPath
}

// BridgeTokenMatches reports whether candidate is the active secret.
func BridgeTokenMatches(candidate string) bool {
	want := BridgeToken()
	if want == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(want)) == 1
}

// BridgeTokenFingerprint is a one-way tag of the active secret. It is only
// returned to callers that already presented the secret.
func BridgeTokenFingerprint() string {
	sum := sha256.Sum256([]byte(BridgeToken()))
	return hex.EncodeToString(sum[:])[:16]
}

// BridgeProof answers a client-supplied nonce with HMAC-SHA256 keyed by the
// bridge secret. This is how a client confirms it is talking to the real server
// WITHOUT handing the secret to a host it has not verified yet: an impostor can
// echo a fingerprint it read from /health, but it cannot produce a proof for a
// nonce it has never seen, and a proof captured earlier is useless because the
// client picks a fresh nonce every time.
func BridgeProof(nonce string) string {
	mac := hmac.New(sha256.New, []byte(BridgeToken()))
	mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidBridgeNonce reports whether a client nonce is well-formed: hex, 16-128
// characters. Anything else is ignored rather than hashed.
func ValidBridgeNonce(nonce string) bool {
	if len(nonce) < 16 || len(nonce) > 128 {
		return false
	}
	_, err := hex.DecodeString(nonce)
	return err == nil
}

// ExtractBridgeToken reads a candidate secret from header, bearer token or
// query string.
func ExtractBridgeToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get(BridgeTokenHeader)); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("Authorization")); len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get(BridgeTokenQuery))
}

// AuthorizeBridgeRequest reports whether a request may read bridge internals or
// write into the cookie pool.
func AuthorizeBridgeRequest(r *http.Request) bool {
	if BridgeTokenDisabled() {
		return true
	}
	return BridgeTokenMatches(ExtractBridgeToken(r))
}

// BridgeOriginAllowed restricts WebSocket handshakes to browser-extension
// contexts and loopback web origins. The upgrader used to return true for every
// Origin, so any page the user visited could open the bridge from their own
// browser and read or overwrite the pooled cookies.
//
// An empty Origin is allowed because extension service workers may omit it; the
// shared secret, not the Origin, is the actual gate.
func BridgeOriginAllowed(origin string) bool {
	o := strings.TrimSpace(strings.ToLower(origin))
	if o == "" {
		return true
	}
	if strings.HasPrefix(o, "chrome-extension://") || strings.HasPrefix(o, "moz-extension://") {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// BridgeBindAddress returns the listen address for the cookie bridge. Defaults
// to all interfaces so the documented multi-device setup keeps working; set
// BRIDGE_BIND=127.0.0.1 to keep the bridge on the local machine only.
func BridgeBindAddress(port int) string {
	host := strings.TrimSpace(os.Getenv(bridgeBindEnv))
	if host == "" {
		host = "0.0.0.0"
	}
	return host + ":" + strconv.Itoa(port)
}

// BridgeIsLoopbackBind reports whether addr keeps the bridge off the network.
func BridgeIsLoopbackBind(addr string) bool {
	host := addr[:strings.LastIndex(addr, ":")]
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// LogBridgeSecurityState prints one startup summary so an operator cannot
// accidentally run an open bridge without noticing.
func LogBridgeSecurityState(addr string) {
	if BridgeTokenDisabled() {
		log.Printf("🚨 %s is set: /api/sync-cookies and the ws:// bridge accept ANY client that can reach them.", bridgeTokenDisable)
		log.Printf("🚨 Anyone on the same network can overwrite the pooled Google cookies. Unset it to re-enable the token gate.")
		return
	}
	token := BridgeToken()
	log.Printf("🔐 Cookie bridge token gate: ENABLED (%s)", BridgeTokenSource())
	if !BridgeIsLoopbackBind(addr) {
		// Not fatal: the bridge is designed to accept syncs from other machines
		// on the user's own network, and the secret is now the gate.
		log.Printf("⚠️  Cookie bridge listening on %s (off-loopback). Clients must present the shared secret; set %s=127.0.0.1 to keep it local.", addr, bridgeBindEnv)
	}
	if len(token) < BridgeMinTokenLength {
		log.Printf("⚠️  The bridge secret is shorter than %d characters — generate a longer one.", BridgeMinTokenLength)
	}
}
