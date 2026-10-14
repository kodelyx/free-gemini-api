package gemini

import (
	"strings"
	"testing"
)

func TestArrangeGeminiOutput_Plain(t *testing.T) {
	raw := "Hello! How can I help you today?"
	res := ArrangeGeminiOutput(raw)
	if res.IsToolCall {
		t.Errorf("Expected IsToolCall to be false")
	}
	if res.CleanText != raw {
		t.Errorf("Expected CleanText %q, got %q", raw, res.CleanText)
	}
	if res.FinishReason != "stop" {
		t.Errorf("Expected finish_reason 'stop', got %q", res.FinishReason)
	}
}

func TestArrangeGeminiOutput_Thinking(t *testing.T) {
	raw := `<thought>
I should explain photosynthesis clearly and concisely.
</thought>
Photosynthesis is the process by which green plants use sunlight to synthesize nutrients.`

	res := ArrangeGeminiOutput(raw)
	if res.IsToolCall {
		t.Errorf("Expected IsToolCall to be false")
	}
	if !strings.Contains(res.ReasoningContent, "photosynthesis clearly") {
		t.Errorf("Expected reasoning content, got %q", res.ReasoningContent)
	}
	if strings.Contains(res.CleanText, "<thought>") {
		t.Errorf("CleanText should not contain thought tags, got %q", res.CleanText)
	}
	if !strings.HasPrefix(res.CleanText, "Photosynthesis") {
		t.Errorf("CleanText should start with 'Photosynthesis', got %q", res.CleanText)
	}
}

func TestArrangeGeminiOutput_ToolCall(t *testing.T) {
	raw := `<thought>
The user wants to know the weather in Tokyo.
</thought>
<tool_call>
{"name": "get_current_weather", "arguments": {"location": "Tokyo", "unit": "celsius"}}
</tool_call>`

	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall {
		t.Fatalf("Expected IsToolCall to be true")
	}
	if res.FinishReason != "tool_calls" {
		t.Errorf("Expected finish_reason 'tool_calls', got %q", res.FinishReason)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Function.Name != "get_current_weather" {
		t.Errorf("Expected function name 'get_current_weather', got %q", tc.Function.Name)
	}
	if !strings.Contains(tc.Function.Arguments, `"Tokyo"`) {
		t.Errorf("Expected arguments to contain 'Tokyo', got %q", tc.Function.Arguments)
	}
	if !strings.HasPrefix(tc.ID, "call_") {
		t.Errorf("Expected ID to start with 'call_', got %q", tc.ID)
	}
}

func TestArrangeGeminiOutput_CodeBlockFallback(t *testing.T) {
	raw := "```json\n{\"name\": \"search_database\", \"parameters\": {\"query\": \"SELECT * FROM users\"}}\n```"
	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall {
		t.Fatalf("Expected IsToolCall to be true via JSON block fallback")
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Function.Name != "search_database" {
		t.Errorf("Expected 'search_database', got %q", res.ToolCalls[0].Function.Name)
	}
}

func TestArrangeGeminiOutput_SandboxCleanup(t *testing.T) {
	raw := "```python?code_reference&code_event_index=1\nprint(45 * 2)\n```\n\n```text?code_stdout&code_event_index=1\n90\n```\n\n90"
	res := ArrangeGeminiOutput(raw)
	if strings.Contains(res.CleanText, "code_reference") {
		t.Errorf("CleanText should not contain internal sandbox params, got %q", res.CleanText)
	}
	if !strings.Contains(res.CleanText, "```python") {
		t.Errorf("Expected cleaned ```python fence, got %q", res.CleanText)
	}
	if !strings.Contains(res.CleanText, "```text") {
		t.Errorf("Expected cleaned ```text fence, got %q", res.CleanText)
	}
}

