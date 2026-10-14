package db

import (
	"os"
	"testing"
)

func TestDB_TierTrackingAndStats(t *testing.T) {
	// Use temporary test database
	testDBPath := "data/test_gemini.db"
	_ = os.Remove(testDBPath)
	defer os.Remove(testDBPath)

	// Initialize DB
	_, err := InitDB()
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	// 1. Log Free message
	err = LogMessageWithTier("conv_1", "user_free", "user", "Hello Free", "gemini-3.8-flash", 100, "free", "acc_free_1")
	if err != nil {
		t.Fatalf("LogMessageWithTier free user failed: %v", err)
	}

	// 2. Log Subscription message
	err = LogMessageWithTier("conv_2", "user_sub", "user", "Hello Advanced Pro", "gemini-3.1-pro", 500, "subscription", "acc_sub_1")
	if err != nil {
		t.Fatalf("LogMessageWithTier subscription user failed: %v", err)
	}

	// 3. Record Account Usages
	_ = RecordAccountUsage("acc_free_1", "cookies/account_1.json", "active", "free")
	_ = RecordAccountUsage("acc_sub_1", "cookies/account_sub.json", "active", "subscription")

	// 4. Check GetAccounts
	accounts, err := GetAccounts()
	if err != nil {
		t.Fatalf("GetAccounts failed: %v", err)
	}
	foundFree := false
	foundSub := false
	for _, acc := range accounts {
		if acc["account_id"] == "acc_free_1" && acc["tier"] == "free" {
			foundFree = true
		}
		if acc["account_id"] == "acc_sub_1" && acc["tier"] == "subscription" {
			foundSub = true
		}
	}
	if !foundFree {
		t.Errorf("Expected acc_free_1 with tier free in GetAccounts")
	}
	if !foundSub {
		t.Errorf("Expected acc_sub_1 with tier subscription in GetAccounts")
	}

	// 5. Check GetSystemStats tier breakdown
	stats, err := GetSystemStats()
	if err != nil {
		t.Fatalf("GetSystemStats failed: %v", err)
	}

	tb, ok := stats["tier_breakdown"].(map[string]any)
	if !ok {
		t.Fatalf("tier_breakdown missing from GetSystemStats: %v", stats)
	}

	fMap := tb["free"].(map[string]any)
	sMap := tb["subscription"].(map[string]any)

	if fMap["messages"].(int) < 1 {
		t.Errorf("Expected >= 1 free message, got %v", fMap["messages"])
	}
	if fMap["tokens"].(int) < 100 {
		t.Errorf("Expected >= 100 free tokens, got %v", fMap["tokens"])
	}

	if sMap["messages"].(int) < 1 {
		t.Errorf("Expected >= 1 subscription message, got %v", sMap["messages"])
	}
	if sMap["tokens"].(int) < 500 {
		t.Errorf("Expected >= 500 subscription tokens, got %v", sMap["tokens"])
	}

	// 6. Check GetAllMessagesForExport
	exportMsgs, err := GetAllMessagesForExport()
	if err != nil {
		t.Fatalf("GetAllMessagesForExport failed: %v", err)
	}
	if len(exportMsgs) < 2 {
		t.Fatalf("Expected at least 2 export messages, got %d", len(exportMsgs))
	}
}
