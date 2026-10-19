package gemini

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetBridgeTokenForTest clears the memoised secret and pins a known value via
// the env override so no test writes cookies/.bridge_token into the repo.
func resetBridgeTokenForTest(t *testing.T, token string) {
	t.Helper()
	bridgeTokenOnce = sync.Once{}
	t.Setenv(bridgeTokenEnv, token)
	bridgeTokenMu.Lock()
	bridgeTokenVal = token
	bridgeTokenPath = "(test)"
	bridgeTokenMu.Unlock()
	t.Cleanup(func() {
		bridgeTokenOnce = sync.Once{}
		bridgeTokenMu.Lock()
		bridgeTokenVal = ""
		bridgeTokenPath = ""
		bridgeTokenMu.Unlock()
	})
}

func TestExtractBridgeTokenSources(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	resetBridgeTokenForTest(t, secret)

	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "header",
			req:  httptest.NewRequest(http.MethodPost, "/api/sync-cookies", nil),
			want: secret,
		},
		{
			name: "bearer",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/api/sync-cookies", nil)
				r.Header.Set("Authorization", "Bearer "+secret)
				return r
			}(),
			want: secret,
		},
		{
			name: "query",
			req:  httptest.NewRequest(http.MethodGet, "/health?token="+secret, nil),
			want: secret,
		},
		{
			name: "absent",
			req:  httptest.NewRequest(http.MethodGet, "/health", nil),
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "header" {
				tc.req.Header.Set(BridgeTokenHeader, secret)
			}
			if got := ExtractBridgeToken(tc.req); got != tc.want {
				t.Fatalf("ExtractBridgeToken() = %q, want %q", got, tc.want)
			}
			if got := AuthorizeBridgeRequest(tc.req); got != (tc.want == secret) {
				t.Fatalf("AuthorizeBridgeRequest() = %v for a %q token", got, tc.want)
			}
		})
	}
}

func TestAuthorizeBridgeRequestRejectsWrongSecrets(t *testing.T) {
	resetBridgeTokenForTest(t, "correct-horse-battery-staple")

	for _, wrong := range []string{"", "correct-horse-battery-stapl", "correct-horse-battery-staplee", "CORRECT-HORSE-BATTERY-STAPLE"} {
		req := httptest.NewRequest(http.MethodPost, "/api/sync-cookies", strings.NewReader("{}"))
		if wrong != "" {
			req.Header.Set(BridgeTokenHeader, wrong)
		}
		if AuthorizeBridgeRequest(req) {
			t.Fatalf("AuthorizeBridgeRequest() accepted %q — a mismatched secret must never open the bridge", wrong)
		}
	}
}

func TestBridgeTokenDisabledEscapeHatch(t *testing.T) {
	resetBridgeTokenForTest(t, "some-secret-value-here")
	t.Setenv(bridgeTokenDisable, "1")

	req := httptest.NewRequest(http.MethodPost, "/api/sync-cookies", strings.NewReader("{}"))
	if !BridgeTokenDisabled() {
		t.Fatal("BRIDGE_TOKEN_DISABLED=1 should disable the gate")
	}
	if !AuthorizeBridgeRequest(req) {
		t.Fatal("the escape hatch must let legacy clients through")
	}
}

// TestBridgeProofMatchesTheChromeExtension locks the cross-language contract:
// the expected value below was produced by Node's crypto.createHmac('sha256', …),
// i.e. the exact computation the extension performs with WebCrypto. If either
// side changes its encoding, pairing breaks and this test fails.
func TestBridgeProofMatchesTheChromeExtension(t *testing.T) {
	const nonce = "00112233445566778899aabbccddeeff"
	resetBridgeTokenForTest(t, "test-bridge-secret-0123456789abcdef")

	// node -e "crypto.createHmac('sha256','test-bridge-secret-0123456789abcdef').update('00112233445566778899aabbccddeeff').digest('hex')"
	const nodeHmacHex = "94ab987f08690607a49ea9e20cebcf5e675c6ed0ff8bfda8b690467c3040830f"

	if got := BridgeProof(nonce); got != nodeHmacHex {
		t.Fatalf("BridgeProof(%q) = %s, want %s (WebCrypto on the extension side produces the second value)", nonce, got, nodeHmacHex)
	}

	// A different secret must not verify — this is the impostor case.
	resetBridgeTokenForTest(t, "a-different-secret-entirely-000000")
	if wrong := BridgeProof(nonce); wrong == nodeHmacHex {
		t.Fatal("an impostor holding a different secret produced the real server's proof")
	}
}

func TestValidBridgeNonce(t *testing.T) {
	valid := []string{"0011223344556677", strings.Repeat("ab", 64)}
	for _, n := range valid {
		if !ValidBridgeNonce(n) {
			t.Fatalf("ValidBridgeNonce(%q) = false, want true", n)
		}
	}
	invalid := []string{"", "short", "zzzzzzzzzzzzzzzz", strings.Repeat("ab", 65)}
	for _, n := range invalid {
		if ValidBridgeNonce(n) {
			t.Fatalf("ValidBridgeNonce(%q) = true, want false", n)
		}
	}
}

func TestBridgeTokenFingerprintIsStableAndSecretDependent(t *testing.T) {
	resetBridgeTokenForTest(t, "secret-one-secret-one-secret-one")
	first := BridgeTokenFingerprint()
	if len(first) != 16 {
		t.Fatalf("fingerprint = %q, want 16 hex chars", first)
	}
	if again := BridgeTokenFingerprint(); again != first {
		t.Fatalf("fingerprint changed between calls: %q then %q", first, again)
	}

	resetBridgeTokenForTest(t, "secret-two-secret-two-secret-two")
	if other := BridgeTokenFingerprint(); other == first {
		t.Fatalf("two different secrets produced the same fingerprint %q", other)
	}
}

func TestInitBridgeTokenPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	// Take the env override out of the picture so this exercises the file path.
	t.Setenv(bridgeTokenEnv, "")
	bridgeTokenOnce = sync.Once{}
	bridgeTokenMu.Lock()
	bridgeTokenVal = ""
	bridgeTokenMu.Unlock()

	generated := InitBridgeToken()
	if len(generated) < BridgeMinTokenLength {
		t.Fatalf("generated token %q is shorter than %d chars", generated, BridgeMinTokenLength)
	}

	info, err := os.Stat(bridgeTokenFileName)
	if err != nil {
		t.Fatalf("token was not persisted to %s: %v", bridgeTokenFileName, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("%s mode = %o, want 600 so other local users cannot read the bridge secret", bridgeTokenFileName, perm)
	}
	if filepath.Dir(bridgeTokenFileName) != "cookies" {
		t.Fatalf("token location changed to %s; the extension docs assume cookies/.bridge_token", bridgeTokenFileName)
	}

	// A restart must reuse the stored secret, otherwise every paired extension
	// breaks on reboot.
	bridgeTokenOnce = sync.Once{}
	bridgeTokenMu.Lock()
	bridgeTokenVal = ""
	bridgeTokenMu.Unlock()
	if reloaded := InitBridgeToken(); reloaded != generated {
		t.Fatalf("reloaded token %q != persisted token %q", reloaded, generated)
	}
}

func TestBridgeOriginAllowed(t *testing.T) {
	allowed := []string{
		"", // extension service workers may omit Origin
		"chrome-extension://abcdefghijklmnopabcdefghijklmnop",
		"moz-extension://1234",
		"http://127.0.0.1:8001",
		"http://localhost:3000",
		"https://127.0.0.1",
	}
	for _, origin := range allowed {
		if !BridgeOriginAllowed(origin) {
			t.Fatalf("BridgeOriginAllowed(%q) = false, want true", origin)
		}
	}

	rejected := []string{
		"https://evil.example.com",
		"http://192.168.1.50",
		"http://10.56.65.136:8001",
		"ftp://127.0.0.1",
		"null",
	}
	for _, origin := range rejected {
		if BridgeOriginAllowed(origin) {
			t.Fatalf("BridgeOriginAllowed(%q) = true — a remote page must not reach the local bridge", origin)
		}
	}
}

func TestBridgeBindAddressDefaultsAndOverride(t *testing.T) {
	t.Setenv(bridgeBindEnv, "")
	if got := BridgeBindAddress(9226); got != "0.0.0.0:9226" {
		t.Fatalf("default bind = %q, want 0.0.0.0:9226 (multi-device sync must keep working)", got)
	}
	if BridgeIsLoopbackBind(BridgeBindAddress(9226)) {
		t.Fatal("0.0.0.0 must not be reported as loopback")
	}

	t.Setenv(bridgeBindEnv, "127.0.0.1")
	if got := BridgeBindAddress(9226); got != "127.0.0.1:9226" {
		t.Fatalf("override bind = %q, want 127.0.0.1:9226", got)
	}
	if !BridgeIsLoopbackBind(BridgeBindAddress(9226)) {
		t.Fatal("127.0.0.1 must be reported as loopback")
	}
}
