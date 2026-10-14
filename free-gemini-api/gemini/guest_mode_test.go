package gemini

import (
	"testing"
)

func TestGuestModeWithoutLogin(t *testing.T) {
	client, err := NewClient("guest")
	if err != nil {
		t.Fatalf("Failed to create guest client: %v", err)
	}

	if client.RawCookies != "" {
		t.Errorf("Expected empty cookies for guest mode, got length %d", len(client.RawCookies))
	}

	if client.AccountTier != "free" {
		t.Errorf("Expected free tier for guest mode, got %s", client.AccountTier)
	}

	err = client.InitSession()
	if err != nil {
		t.Fatalf("Failed to init guest session: %v", err)
	}

	resp, err := client.AskWithToolModel("Reply with 'Guest Mode OK' and nothing else.", "", "3.5-flash-lite")
	if err != nil {
		t.Fatalf("Guest AskWithToolModel failed: %v", err)
	}

	t.Logf("Guest response: %s", resp.Text)
	if resp.Text == "" {
		t.Errorf("Expected non-empty response in guest mode")
	}
}
