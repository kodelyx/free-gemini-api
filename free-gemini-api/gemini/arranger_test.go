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

func TestArrangeGeminiOutput_AutoLinkifiedURLArguments(t *testing.T) {
	// Gemini's web stream renders bare URLs as "[https://x](https://x)".
	// That markdown must not survive into tool-call arguments.
	raw := "<tool_call>\n{\"name\": \"browser_open\", \"arguments\": {\"url\": \"[https://www.reddit.com/r/Eyeballs](https://www.reddit.com/r/Eyeballs)\"}}\n</tool_call>"
	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall {
		t.Fatalf("Expected IsToolCall to be true")
	}
	args := res.ToolCalls[0].Function.Arguments
	if strings.Contains(args, "](") {
		t.Fatalf("Arguments still contain markdown link syntax: %q", args)
	}
	if !strings.Contains(args, `"https://www.reddit.com/r/Eyeballs"`) {
		t.Errorf("Expected clean URL in arguments, got %q", args)
	}
}

func TestArrangeGeminiOutput_DirectArgumentKeys(t *testing.T) {
	// Some models put the argument fields at the top level instead of
	// nesting them under "arguments".
	raw := `{"name": "execute", "code": "console.log(2+2)"}`
	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall {
		t.Fatalf("Expected IsToolCall to be true for direct-argument JSON")
	}
	if res.ToolCalls[0].Function.Name != "execute" {
		t.Errorf("Expected 'execute', got %q", res.ToolCalls[0].Function.Name)
	}
	if !strings.Contains(res.ToolCalls[0].Function.Arguments, `"code":"console.log(2+2)"`) {
		t.Errorf("Expected code argument, got %q", res.ToolCalls[0].Function.Arguments)
	}
}

func TestArrangeGeminiOutput_ConcatenatedToolCalls(t *testing.T) {
	// A truncated first attempt followed by the complete call.
	raw := `{"name": "execute", "code": "const tab = await tools.open({ url: "{"name": "execute", "code": "run().done()"}`
	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall {
		t.Fatalf("Expected IsToolCall to be true for concatenated JSON")
	}
	if !strings.Contains(res.ToolCalls[0].Function.Arguments, "run().done()") {
		t.Errorf("Expected the complete object to be used, got %q", res.ToolCalls[0].Function.Arguments)
	}
}

func TestArrangeGeminiOutput_CleanTextUnwrapsAutoLinks(t *testing.T) {
	raw := "See [https://example.com/path](https://example.com/path) for details."
	res := ArrangeGeminiOutput(raw)
	if strings.Contains(res.CleanText, "](") {
		t.Errorf("CleanText should not contain the auto-link artifact, got %q", res.CleanText)
	}
	if !strings.Contains(res.CleanText, "https://example.com/path") {
		t.Errorf("CleanText should keep the bare URL, got %q", res.CleanText)
	}
}

func TestArrangeGeminiOutput_GenuineMarkdownLinkKept(t *testing.T) {
	raw := "Read the [docs](https://example.com/docs) page."
	res := ArrangeGeminiOutput(raw)
	if !strings.Contains(res.CleanText, "[docs](https://example.com/docs)") {
		t.Errorf("Genuine markdown link (label != href) should be preserved, got %q", res.CleanText)
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
