package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const wsTestSecret = "ws-test-secret-0123456789abcdef"

// startBridgeForTest boots the real cookie WebSocket server on a free port and
// waits until it accepts connections.
func startBridgeForTest(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	go StartCookieWebSocketServer(port)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return port
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("cookie bridge never started listening on port %d", port)
	return 0
}

func dialBridge(t *testing.T, port int, query string, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := fmt.Sprintf("ws://127.0.0.1:%d/%s", port, query)
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	return dialer.Dial(url, hdr)
}

// readFrameWithTimeout guards the tests against a server that simply hangs.
func readFrameWithTimeout(t *testing.T, conn *websocket.Conn) (map[string]any, error) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode %q: %w", string(data), err)
	}
	return out, nil
}

func cookieFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("cookies", "account_*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return files
}

// TestCookieBridgeRejectsUnauthenticatedClients is the regression test for
// issue #1: before the shared secret existed, this exact exchange — connect to
// ws://<host>:9226 and send a cookies_payload — wrote the payload into the
// worker pool with no authentication whatsoever. Anyone on the LAN who answered
// the extension's /health probe could do it.
func TestCookieBridgeRejectsUnauthenticatedClients(t *testing.T) {
	t.Chdir(t.TempDir())
	resetBridgeTokenForTest(t, wsTestSecret)
	port := startBridgeForTest(t)

	payload := []CookieObject{
		{Domain: ".google.com", Name: "__Secure-1PSID", Value: "stolen-session-token", Path: "/"},
		{Domain: ".google.com", Name: "SAPISID", Value: "stolen-sapisid", Path: "/"},
	}
	body, err := json.Marshal(ExtensionMessage{Type: "cookies_payload", Cookies: payload})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		query string
		token string
		want  string
	}{
		// A client that opens the socket and immediately pushes cookies without
		// ever presenting a secret is rejected as unauthorized; the distinct
		// "authentication_required" reason is sent when the client goes silent
		// and the 5s auth deadline expires.
		{name: "no token at all", query: "", token: "", want: "unauthorized"},
		{name: "wrong token in first frame", query: "", token: "not-the-secret-at-all", want: "unauthorized"},
		{name: "wrong token in query string", query: "?token=nope-nope-nope-nope", token: "", want: "unauthorized"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, _, err := dialBridge(t, port, tc.query, "")
			if err != nil {
				// A handshake-level rejection is just as good as a frame-level one.
				return
			}
			defer conn.Close()

			if tc.token != "" {
				if err := conn.WriteJSON(ExtensionMessage{Type: "auth", Token: tc.token}); err != nil {
					t.Fatalf("write auth: %v", err)
				}
			} else if tc.query == "" {
				// Straight to the payload, as an attacker would.
				if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
					t.Fatalf("write payload: %v", err)
				}
			}

			msg, err := readFrameWithTimeout(t, conn)
			if err != nil {
				t.Logf("connection closed without a frame (%v) — acceptable for %s", err, tc.name)
			} else if msg["type"] == "auth_ok" {
				t.Fatalf("%s was authenticated: %v", tc.name, msg)
			} else if got := msg["error"]; got != tc.want {
				t.Fatalf("error frame = %v, want %q", msg, tc.want)
			}
		})
	}

	if files := cookieFiles(t); len(files) != 0 {
		t.Fatalf("unauthenticated clients wrote %v into the cookie pool", files)
	}
}

// TestCookieBridgeAcceptsPairedExtension walks the handshake the fixed Chrome
// extension performs, and pins the fingerprint contract it relies on
// (sha256(token) hex, first 16 chars) so the JS and Go sides cannot drift.
func TestCookieBridgeAcceptsPairedExtension(t *testing.T) {
	t.Chdir(t.TempDir())
	resetBridgeTokenForTest(t, wsTestSecret)
	port := startBridgeForTest(t)

	conn, _, err := dialBridge(t, port, "", "")
	if err != nil {
		t.Fatalf("paired client could not connect: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(ExtensionMessage{Type: "auth", Token: wsTestSecret}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	ack, err := readFrameWithTimeout(t, conn)
	if err != nil {
		t.Fatalf("no auth_ok frame: %v", err)
	}
	if ack["type"] != "auth_ok" {
		t.Fatalf("first frame = %v, want auth_ok", ack)
	}

	sum := sha256.Sum256([]byte(wsTestSecret))
	wantFingerprint := hex.EncodeToString(sum[:])[:16]
	if ack["fingerprint"] != wantFingerprint {
		t.Fatalf("fingerprint = %v, want %s (the extension compares exactly this)", ack["fingerprint"], wantFingerprint)
	}

	// Legacy pings still work on an authenticated channel.
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	pong, err := readFrameWithTimeout(t, conn)
	if err != nil || pong["type"] != "pong" {
		t.Fatalf("ping -> %v (err %v), want pong", pong, err)
	}

	// And a real payload now lands in the pool.
	payload := []CookieObject{
		{Domain: ".google.com", Name: "__Secure-1PSID", Value: "paired-session-token", Path: "/"},
		{Domain: ".google.com", Name: "SAPISID", Value: "paired-sapisid", Path: "/"},
	}
	if err := conn.WriteJSON(ExtensionMessage{Type: "cookies_payload", Token: wsTestSecret, Cookies: payload}); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var files []string
	for time.Now().Before(deadline) {
		if files = cookieFiles(t); len(files) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(files) == 0 {
		t.Fatal("authenticated payload was not persisted")
	}

	saved, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "paired-session-token") {
		t.Fatalf("saved cookie file %s does not contain the synced session", files[0])
	}
}

// TestCookieBridgeRejectsForeignOrigin covers the second half of issue #1: any
// page the user visited could drive the local bridge from their own browser
// because the upgrader accepted every Origin.
func TestCookieBridgeRejectsForeignOrigin(t *testing.T) {
	t.Chdir(t.TempDir())
	resetBridgeTokenForTest(t, wsTestSecret)
	port := startBridgeForTest(t)

	conn, _, err := dialBridge(t, port, "?token="+wsTestSecret, "http://192.168.1.50")
	if err == nil {
		conn.Close()
		t.Fatal("handshake from a LAN web origin was accepted")
	}

	// A legitimate extension context still connects.
	conn, _, err = dialBridge(t, port, "?token="+wsTestSecret, "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
	if err != nil {
		t.Fatalf("chrome-extension origin was rejected: %v", err)
	}
	defer conn.Close()

	ack, err := readFrameWithTimeout(t, conn)
	if err != nil || ack["type"] != "auth_ok" {
		t.Fatalf("query-string token was not accepted: %v (err %v)", ack, err)
	}
}
