package gemini

import (
	"strings"
	"testing"
)

func TestModelRegistryResolve(t *testing.T) {
	reg := GetModelRegistry()

	tests := []struct {
		input          string
		expectedID     string
		expectedHex    string
		headerContains string // what to check in BuildHeaderValue (empty = use expectedHex)
	}{
		{"gemini-3.8-flash", "gemini-3.8-flash", "56fdd199312815e2", ""},
		{"flash", "gemini-3.8-flash", "56fdd199312815e2", ""},
		{"gemini-flash", "gemini-3.8-flash", "56fdd199312815e2", ""},
		{"gemini-3.5-flash-lite", "gemini-3.5-flash-lite", "8c46e95b1a07cecc", ""},
		{"flash-lite", "gemini-3.5-flash-lite", "8c46e95b1a07cecc", ""},
		{"lite", "gemini-3.5-flash-lite", "8c46e95b1a07cecc", ""},
		{"gemini-3.1-pro", "gemini-3.1-pro", "e6fa609c3fa255c0", ""},
		{"gemini-pro", "gemini-3.1-pro", "e6fa609c3fa255c0", ""},
		{"pro", "gemini-3.1-pro", "e6fa609c3fa255c0", ""},
		// Argon models: ModeID is NOT embedded in the JSPB header (verified via CDP 2026-10-14).
		// Thinking level sits at index 15: Low=1, Medium=4, High=2
		{"gemini-4-argon", "gemini-4-argon", "95a4c0abcba24364", ",8,1,"},
		{"argon", "gemini-4-argon", "95a4c0abcba24364", ",8,1,"},
		{"argon-high", "gemini-4-argon-thinking-high", "95a4c0abcba24364", ",8,2,"},
		{"argon-medium", "gemini-4-argon-thinking-medium", "95a4c0abcba24364", ",8,4,"},
		{"argon-low", "gemini-4-argon-thinking-low", "95a4c0abcba24364", ",8,1,"},
		{"thinking", "gemini-3.8-thinking", "56fdd199312815e2", ""},
		{"", "gemini-3.8-flash", "56fdd199312815e2", ""},
		{"unknown-model-xyz", "gemini-3.8-flash", "56fdd199312815e2", ""},
	}

	for _, tt := range tests {
		m := reg.Resolve(tt.input)
		if m == nil {
			t.Fatalf("Resolve(%q) returned nil", tt.input)
		}
		if m.ID != tt.expectedID {
			t.Errorf("Resolve(%q) ID = %v, expected %v", tt.input, m.ID, tt.expectedID)
		}
		if m.ModeID != tt.expectedHex {
			t.Errorf("Resolve(%q) ModeID = %v, expected %v", tt.input, m.ModeID, tt.expectedHex)
		}

		headerVal := m.BuildHeaderValue()
		checkStr := tt.headerContains
		if checkStr == "" {
			checkStr = tt.expectedHex
		}
		if !strings.Contains(headerVal, checkStr) {
			t.Errorf("BuildHeaderValue() %q does not contain %q", headerVal, checkStr)
		}
	}
}

func TestModelRegistryCustomRegister(t *testing.T) {
	reg := GetModelRegistry()

	custom := ModelConfig{
		ID:          "gemini-test-custom",
		DisplayName: "Gemini Custom Model",
		ModeID:      "12345678abcdef",
		Aliases:     []string{"custom-test"},
		Source:      "custom",
	}

	reg.Register(custom)
	defer reg.Unregister("gemini-test-custom")

	resolved := reg.Resolve("custom-test")
	if resolved.ID != "gemini-test-custom" {
		t.Fatalf("Expected resolved custom model ID 'gemini-test-custom', got %s", resolved.ID)
	}
	if resolved.ModeID != "12345678abcdef" {
		t.Fatalf("Expected ModeID '12345678abcdef', got %s", resolved.ModeID)
	}
}

// TestArgonThinkingLevels verifies that the JSPB header thinking level values
// match what was observed in live CDP traffic (2026-10-14):
//
//	Low=1, Medium=4, High=2  (at index 15)
func TestArgonThinkingLevels(t *testing.T) {
	reg := GetModelRegistry()

	cases := []struct {
		model     string
		wantLevel string
	}{
		{"argon-low", ",8,1,"},
		{"argon-medium", ",8,4,"},
		{"argon-high", ",8,2,"},
	}

	for _, c := range cases {
		m := reg.Resolve(c.model)
		if m == nil {
			t.Fatalf("Resolve(%q) returned nil", c.model)
		}
		h := m.BuildHeaderValue()
		if !strings.Contains(h, c.wantLevel) {
			t.Errorf("Resolve(%q).BuildHeaderValue() = %q, want to contain %q", c.model, h, c.wantLevel)
		}
		// Argon thinking headers contain ModeID at index 4 and thinking level at index 15
		if !strings.Contains(h, "95a4c0abcba24364") {
			t.Errorf("Resolve(%q).BuildHeaderValue() = %q, should embed ModeID at index 4", c.model, h)
		}
	}
}
