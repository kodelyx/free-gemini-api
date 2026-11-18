package gemini

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ToolCall represents an OpenAI-compatible function tool call
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // stringified JSON object
}

// ArrangedOutput contains the parsed, cleaned, and structured result of Gemini's response
type ArrangedOutput struct {
	CleanText        string     `json:"clean_text"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	IsToolCall       bool       `json:"is_tool_call"`
	FinishReason     string     `json:"finish_reason"` // "tool_calls" or "stop"
}

// FormatToolsInstruction creates a clear, structured system prompt injecting tools into Gemini
func FormatToolsInstruction(tools []map[string]any) string {
	if len(tools) == 0 {
		return ""
	}

	toolsJSON, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		bytes, _ := json.Marshal(tools)
		toolsJSON = bytes
	}

	var sb strings.Builder
	sb.WriteString("\n[AVAILABLE TOOLS]\n")
	sb.WriteString("You have access to the following tools that you can invoke:\n")
	sb.WriteString("```json\n")
	sb.Write(toolsJSON)
	sb.WriteString("\n```\n\n")
	sb.WriteString("[TOOL CALL INSTRUCTIONS]\n")
	sb.WriteString("1. If you need to call a tool to answer the user's request, reply ONLY with one or more `<tool_call>` blocks in this exact format:\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "<tool_name>", "arguments": {<json_arguments>}}` + "\n")
	sb.WriteString("</tool_call>\n")
	sb.WriteString("2. You can invoke multiple tools by outputting multiple `<tool_call>` blocks consecutively.\n")
	sb.WriteString("3. Ensure the \"arguments\" object strictly follows the schema parameters (types, names, requirements).\n")
	sb.WriteString("4. If no tool is needed, or after receiving tool results, respond directly in natural language without any `<tool_call>` tags.\n")
	sb.WriteString("5. Do not invent tools that are not in the list.\n")
	sb.WriteString("6. You do not execute tools yourself — the host application performs every action. Never refuse to emit a <tool_call> because you believe you cannot browse the web, open a site, or run code: emit the call and the host will perform it.\n\n")

	return sb.String()
}

var (
	thoughtRegex  = regexp.MustCompile(`(?s)<(?:thought|thinking)>([\s\S]*?)</(?:thought|thinking)>`)
	toolCallRegex = regexp.MustCompile(`(?s)<tool_call>([\s\S]*?)</tool_call>`)
	jsonCodeBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
	// Gemini web auto-linkifies bare URLs in its rendered stream, producing
	// "[https://x](https://x)" instead of the plain URL. Inside tool-call
	// arguments that extra markdown syntax corrupts the JSON payload.
	autoLinkRegex = regexp.MustCompile(`\[(https?://[^\]]+)\]\((https?://[^)]+)\)`)
)

// deLinkifyAutoLinks rewrites Gemini's auto-linkified anchors back to the bare
// URL when the label and href are identical (the auto-link artifact shape).
// A genuine markdown link whose label differs from its href is left intact.
func deLinkifyAutoLinks(s string) string {
	return autoLinkRegex.ReplaceAllStringFunc(s, func(m string) string {
		sub := autoLinkRegex.FindStringSubmatch(m)
		if len(sub) == 3 && sub[1] == sub[2] {
			return sub[1]
		}
		return m
	})
}

// ArrangeGeminiOutput analyzes, cleans, parses, and structures raw Gemini output
func ArrangeGeminiOutput(raw string) *ArrangedOutput {
	res := &ArrangedOutput{
		FinishReason: "stop",
	}

	text := raw

	// 1. Extract and separate Thinking / Reasoning blocks (<thought> or <thinking>)
	if matches := thoughtRegex.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		var thoughts []string
		for _, m := range matches {
			if len(m) > 1 {
				t := strings.TrimSpace(m[1])
				if t != "" {
					thoughts = append(thoughts, t)
				}
			}
		}
		if len(thoughts) > 0 {
			res.ReasoningContent = strings.Join(thoughts, "\n\n")
		}
		text = thoughtRegex.ReplaceAllString(text, "")
	}

	text = strings.TrimSpace(text)

	// Gemini's web stream auto-linkifies bare URLs ("[https://x](https://x)").
	// Undo that so tool-call arguments keep the plain URL.
	text = deLinkifyAutoLinks(text)

	// 2. Extract Tool Calls from <tool_call> tags
	toolCallMatches := toolCallRegex.FindAllStringSubmatch(text, -1)
	if len(toolCallMatches) > 0 {
		for _, m := range toolCallMatches {
			if len(m) > 1 {
				payload := strings.TrimSpace(m[1])
				tc, ok := parseSingleToolCall(payload)
				if ok {
					res.ToolCalls = append(res.ToolCalls, tc)
				}
			}
		}
		if len(res.ToolCalls) > 0 {
			res.IsToolCall = true
			res.FinishReason = "tool_calls"
			clean := toolCallRegex.ReplaceAllString(text, "")
			res.CleanText = strings.TrimSpace(clean)
			return res
		}
	}

	// 3. Fallback: Check if response contains a direct JSON tool call inside code block or raw JSON
	if strings.Contains(text, `"name"`) {
		// Try code block first
		if cbMatches := jsonCodeBlock.FindAllStringSubmatch(text, -1); len(cbMatches) > 0 {
			for _, m := range cbMatches {
				if len(m) > 1 {
					payload := strings.TrimSpace(m[1])
					tc, ok := parseSingleToolCall(payload)
					if ok {
						res.ToolCalls = append(res.ToolCalls, tc)
					}
				}
			}
		}
		// If still none, try entire text as JSON
		if len(res.ToolCalls) == 0 {
			tc, ok := parseSingleToolCall(text)
			if ok {
				res.ToolCalls = append(res.ToolCalls, tc)
			}
		}
		// If still none, scan for the first balanced JSON object that parses as
		// a tool call. Models sometimes emit overlapping/concatenated objects
		// (e.g. a truncated attempt immediately followed by the full one), and
		// sometimes put the argument fields at the top level ({"name": "x",
		// "code": "..."}) instead of nesting them under "arguments".
		if len(res.ToolCalls) == 0 {
			if tc, ok := findFirstJSONToolCall(text); ok {
				res.ToolCalls = append(res.ToolCalls, tc)
			}
		}

		if len(res.ToolCalls) > 0 {
			res.IsToolCall = true
			res.FinishReason = "tool_calls"
			res.CleanText = ""
			return res
		}
	}

	// 4. Clean up any raw internal artifacts or trailing disclaimers
	res.CleanText = sanitizeGeminiContent(text)
	return res
}

func parseSingleToolCall(payload string) (ToolCall, bool) {
	payload = strings.TrimSpace(payload)
	// Remove outer backticks if any
	payload = strings.TrimPrefix(payload, "```json")
	payload = strings.TrimPrefix(payload, "```")
	payload = strings.TrimSuffix(payload, "```")
	payload = strings.TrimSpace(payload)
	// Undo Gemini's auto-linkified URLs so URL arguments stay intact.
	payload = deLinkifyAutoLinks(payload)

	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return ToolCall{}, false
	}

	// Extract tool name
	name := ""
	if n, ok := obj["name"].(string); ok && n != "" {
		name = n
	} else if n, ok := obj["tool"].(string); ok && n != "" {
		name = n
	} else if n, ok := obj["function"].(string); ok && n != "" {
		name = n
	}

	if name == "" {
		return ToolCall{}, false
	}

	// Extract arguments
	argsStr := "{}"
	if argsObj, exists := obj["arguments"]; exists {
		if asStr, ok := argsObj.(string); ok {
			argsStr = asStr
		} else {
			bytes, err := json.Marshal(argsObj)
			if err == nil {
				argsStr = string(bytes)
			}
		}
	} else if paramsObj, exists := obj["parameters"]; exists {
		if asStr, ok := paramsObj.(string); ok {
			argsStr = asStr
		} else {
			bytes, err := json.Marshal(paramsObj)
			if err == nil {
				argsStr = string(bytes)
			}
		}
	}

	// Fallback: models sometimes put the argument fields at the top level,
	// either instead of an "arguments"/"parameters" object
	// ({"name": "execute", "code": "..."}) or alongside an empty one.
	// Collect the remaining non-meta keys as the arguments object.
	if argsStr == "{}" {
		rest := map[string]any{}
		for k, v := range obj {
			switch k {
			case "name", "tool", "function", "id", "type", "arguments", "parameters":
				continue
			}
			rest[k] = v
		}
		if len(rest) > 0 {
			if bytes, err := json.Marshal(rest); err == nil {
				argsStr = string(bytes)
			}
		}
	}

	// Ensure argsStr is valid JSON
	var dummy any
	if err := json.Unmarshal([]byte(argsStr), &dummy); err != nil {
		argsStr = "{}"
	}

	return ToolCall{
		ID:   "call_" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		Type: "function",
		Function: ToolCallFunction{
			Name:      name,
			Arguments: argsStr,
		},
	}, true
}

// findFirstJSONToolCall scans text for balanced {...} objects and returns the
// first that parses as a tool call. Handles overlapping or concatenated JSON
// emissions (e.g. a truncated attempt immediately followed by the full one).
func findFirstJSONToolCall(text string) (ToolCall, bool) {
	candidates := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if candidates >= 50 {
			break
		}
		candidates++
		objStr, ok := balancedJSONObjectAt(text, i)
		if !ok {
			continue
		}
		if tc, ok := parseSingleToolCall(objStr); ok {
			return tc, true
		}
	}
	return ToolCall{}, false
}

// balancedJSONObjectAt returns the substring starting at s[start] ('{') up to
// and including its balanced closing '}', honouring JSON string escapes.
func balancedJSONObjectAt(s string, start int) (string, bool) {
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}


var (
	codeFenceWithQueryRegex = regexp.MustCompile(`(?m)^` + "```" + `([a-zA-Z0-9_-]+)\?[^\n]*$`)
	multiNewlineRegex       = regexp.MustCompile(`\n{3,}`)
)

func sanitizeGeminiContent(text string) string {
	cleaned := text
	// Remove null bytes
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")
	// Clean Google Python Sandbox internal query strings: ```python?code_reference&code_event_index=1 -> ```python
	cleaned = codeFenceWithQueryRegex.ReplaceAllString(cleaned, "```$1")
	// Normalize excessive newlines
	cleaned = multiNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}
