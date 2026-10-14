package gemini

import (
	"testing"
)

func TestDetectAccountTier(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		filename string
		expected string
	}{
		{
			name:     "Advanced via HTML is_advanced",
			html:     `<html><script>window.WIZ_global_data = {"is_advanced":true};</script></html>`,
			filename: "account_1.json",
			expected: "subscription",
		},
		{
			name:     "Advanced via HTML is_subscriber",
			html:     `<html><script>window.WIZ_global_data = {"is_subscriber":true};</script></html>`,
			filename: "account_2.json",
			expected: "subscription",
		},
		{
			name:     "Advanced via Filename",
			html:     `<html>Free tier interface</html>`,
			filename: "account_pro_sub.json",
			expected: "subscription",
		},
		{
			name:     "Free Tier Account",
			html:     `<html>Welcome to Gemini 3.8 Flash free edition</html>`,
			filename: "account_free_user.json",
			expected: "free",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := DetectAccountTier(tt.html, tt.filename)
			if tier != tt.expected {
				t.Errorf("DetectAccountTier() = %s, expected %s", tier, tt.expected)
			}
		})
	}
}

func TestDiscoverModelsFromHTML(t *testing.T) {
	htmlSample := `

		<script>
			const config = {
				"model": "56fdd199312815e2",
				"pro_mode": "61530e79959ab139",
				"flash_alt": "fbb127bbb056c959"
			};
		</script>
	`

	discovered := DiscoverModelsFromHTML(htmlSample)
	if len(discovered) < 3 {
		t.Fatalf("Expected at least 3 models discovered, got %d", len(discovered))
	}

	foundPro := false
	foundFlash := false
	for _, m := range discovered {
		if m.ID == "gemini-3.1-pro" && m.MinTier == "subscription" {
			foundPro = true
		}
		if m.ID == "gemini-3.8-flash" && m.MinTier == "free" {
			foundFlash = true
		}
	}

	if !foundPro {
		t.Errorf("Did not discover gemini-3.1-pro with subscription tier")
	}
	if !foundFlash {
		t.Errorf("Did not discover gemini-3.8-flash with free tier")
	}
}
