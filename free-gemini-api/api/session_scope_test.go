package api

import (
	"encoding/json"
	"testing"
)

func TestFirstUserMessageContent(t *testing.T) {
	msgs := []OpenAIChatMessage{
		{Role: "system", Content: json.RawMessage(`"system prompt"`)},
		{Role: "user", Content: json.RawMessage(`"open reddit please"`)},
		{Role: "assistant", Content: json.RawMessage(`"sure"`)},
		{Role: "user", Content: json.RawMessage(`"again"`)},
	}
	if got := firstUserMessageContent(msgs); got != "open reddit please" {
		t.Fatalf("expected first user message, got %q", got)
	}

	parts := []OpenAIChatMessage{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hello from parts"}]`)},
	}
	if got := firstUserMessageContent(parts); got != "hello from parts" {
		t.Fatalf("expected flattened content parts, got %q", got)
	}

	if got := firstUserMessageContent(nil); got != "" {
		t.Fatalf("expected empty for no messages, got %q", got)
	}
}

func TestContentScopeKey_PerChatStability(t *testing.T) {
	// The same opening message must yield the same scope across turns, and
	// different chats must not share an upstream conversation.
	a := contentScopeKey("open reddit in the browser")
	if a == "" || a != contentScopeKey("open reddit in the browser") {
		t.Fatalf("scope should be stable for identical content")
	}
	if a == contentScopeKey("what is 12*11?") {
		t.Fatalf("different chats must produce different scopes")
	}
}
