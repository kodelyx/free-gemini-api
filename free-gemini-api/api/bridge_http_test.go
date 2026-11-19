package api

import (
	"encoding/json"
	"goapi/gemini"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// newBridgeApp builds the real route table so the checks below exercise the
// shipped handlers rather than a hand-rolled stand-in.
func newBridgeApp() *fiber.App {
	app := fiber.New()
	RegisterRoutes(app)
	return app
}

// bridgeSecretForTest returns the secret the server actually minted. Tests run
// in a temp dir, so the generated cookies/.bridge_token never touches the repo.
func bridgeSecretForTest(t *testing.T) string {
	t.Helper()
	t.Setenv("BRIDGE_TOKEN", "")
	t.Setenv("BRIDGE_TOKEN_DISABLED", "")
	secret := gemini.BridgeToken()
	if len(secret) < gemini.BridgeMinTokenLength {
		t.Fatalf("server minted a %d-char secret, want at least %d", len(secret), gemini.BridgeMinTokenLength)
	}
	return secret
}

func doJSON(t *testing.T, app *fiber.App, method, target, token, body string) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(gemini.BridgeTokenHeader, token)
	}

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp, out
}

// TestSyncCookiesRequiresBridgeToken is the HTTP half of the issue #1
// regression: POST /api/sync-cookies used to accept a full Google session from
// anyone who could reach the port and write it straight into the worker pool.
func TestSyncCookiesRequiresBridgeToken(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newBridgeApp()
	secret := bridgeSecretForTest(t)

	payload := `{"cookies":[{"domain":".google.com","name":"__Secure-1PSID","value":"attacker-session","path":"/"}]}`

	resp, body := doJSON(t, app, http.MethodPost, "/api/sync-cookies", "", payload)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tokenless POST /api/sync-cookies = %d (%v), want 401", resp.StatusCode, body)
	}

	resp, _ = doJSON(t, app, http.MethodPost, "/api/sync-cookies", "guessed-wrong-token-1234", payload)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token POST /api/sync-cookies = %d, want 401", resp.StatusCode)
	}

	resp, body = doJSON(t, app, http.MethodPost, "/api/sync-cookies?token="+secret, "", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query-token POST /api/sync-cookies = %d (%v), want 200", resp.StatusCode, body)
	}

	resp, body = doJSON(t, app, http.MethodPost, "/api/sync-cookies", secret, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("headered POST /api/sync-cookies = %d (%v), want 200", resp.StatusCode, body)
	}
	if body["status"] != "success" {
		t.Fatalf("sync response = %v, want status success", body)
	}
}

// TestHealthHidesPoolInternalsFromStrangers pins the reduced /health payload:
// the old response advertised the account count and cookie TTL to anyone, which
// turned a LAN scan into a fingerprint of a live cookie farm.
func TestHealthHidesPoolInternalsFromStrangers(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newBridgeApp()
	secret := bridgeSecretForTest(t)

	resp, body := doJSON(t, app, http.MethodGet, "/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health = %d, want 200 (the extension still needs reachability)", resp.StatusCode)
	}
	if body["status"] != "online" {
		t.Fatalf("/health status = %v, want online", body["status"])
	}
	for _, leaked := range []string{"cookie_health", "active_extension_workers", "cookie_accounts_in_pool", "bridge_token_fingerprint"} {
		if _, present := body[leaked]; present {
			t.Fatalf("/health leaked %q to an unauthenticated caller: %v", leaked, body)
		}
	}

	resp, body = doJSON(t, app, http.MethodGet, "/health", secret, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /health = %d, want 200", resp.StatusCode)
	}
	if body["bridge_auth"] != "ok" {
		t.Fatalf("authenticated /health bridge_auth = %v, want ok", body["bridge_auth"])
	}
	if body["bridge_token_fingerprint"] != gemini.BridgeTokenFingerprint() {
		t.Fatalf("authenticated /health fingerprint = %v, want %s", body["bridge_token_fingerprint"], gemini.BridgeTokenFingerprint())
	}
	if _, present := body["cookie_health"]; !present {
		t.Fatalf("authenticated /health should include pool internals: %v", body)
	}
}

// TestHealthAnswersNonceChallenge documents the identity handshake the extension
// uses before it will transmit anything: it sends a nonce and the server must
// return HMAC-SHA256(token, nonce). The secret itself is never sent, so an
// impostor answering /health cannot collect it, and a captured proof is useless
// because the client picks a new nonce every probe.
func TestHealthAnswersNonceChallenge(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newBridgeApp()
	secret := bridgeSecretForTest(t)

	nonce := "00112233445566778899aabbccddeeff"
	resp, body := doJSON(t, app, http.MethodGet, "/health?nonce="+nonce, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health?nonce= = %d, want 200", resp.StatusCode)
	}
	if body["bridge_proof"] != gemini.BridgeProof(nonce) {
		t.Fatalf("bridge_proof = %v, want %s", body["bridge_proof"], gemini.BridgeProof(nonce))
	}
	if body["bridge_auth"] != "required" {
		t.Fatalf("unauthenticated challenge response bridge_auth = %v, want required", body["bridge_auth"])
	}
	if strings.Contains(body["bridge_proof"].(string), secret) {
		t.Fatal("the proof leaks the secret")
	}
	if _, present := body["cookie_health"]; present {
		t.Fatalf("the challenge response leaked pool internals: %v", body)
	}

	// Garbage nonces are ignored rather than hashed.
	_, body = doJSON(t, app, http.MethodGet, "/health?nonce=not-hex", "", "")
	if _, present := body["bridge_proof"]; present {
		t.Fatalf("a malformed nonce produced a proof: %v", body)
	}
}
