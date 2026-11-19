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
	return FormatToolsInstructionWithChoice(tools, nil)
}

// FormatToolsInstructionWithChoice formats tool definitions with tool_choice enforcement ("none", "auto", "required", or specific function)
func FormatToolsInstructionWithChoice(tools []map[string]any, toolChoice interface{}) string {
	if len(tools) == 0 {
		return ""
	}
	if s, ok := toolChoice.(string); ok && strings.EqualFold(strings.TrimSpace(s), "none") {
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

	if choiceNote := formatToolChoiceConstraint(toolChoice); choiceNote != "" {
		sb.WriteString("6. " + choiceNote + "\n")
	}
	sb.WriteString("\n")

	return sb.String()
}

func formatToolChoiceConstraint(toolChoice interface{}) string {
	switch v := toolChoice.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "required") {
			return "CRITICAL: You MUST call at least one tool from the list above. Do not respond with plain text alone."
		}
	case map[string]any:
		if fnMap, ok := v["function"].(map[string]any); ok {
			if name, ok := fnMap["name"].(string); ok && strings.TrimSpace(name) != "" {
				return "CRITICAL: You MUST call the exact tool named \"" + strings.TrimSpace(name) + "\"."
			}
		}
		if name, ok := v["name"].(string); ok && strings.TrimSpace(name) != "" {
			return "CRITICAL: You MUST call the exact tool named \"" + strings.TrimSpace(name) + "\"."
		}
	}
	return ""
}

var (
	thoughtRegex  = regexp.MustCompile(`(?s)<(?:thought|thinking)>([\s\S]*?)</(?:thought|thinking)>`)
	toolCallRegex = regexp.MustCompile(`(?s)<tool_call>([\s\S]*?)</tool_call>`)
	jsonCodeBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
)

func extractThoughtBlocks(raw string) (reasoning string, cleanedText string) {
	text := raw
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
			reasoning = strings.Join(thoughts, "\n\n")
		}
		text = thoughtRegex.ReplaceAllString(text, "")
	}
	return reasoning, strings.TrimSpace(text)
}

// ArrangeGeminiOutputTextOnly cleans raw Gemini output without parsing tool calls (used when no tools are active)
func ArrangeGeminiOutputTextOnly(raw string) *ArrangedOutput {
	reasoning, text := extractThoughtBlocks(raw)
	return &ArrangedOutput{
		CleanText:        sanitizeGeminiContent(text),
		ReasoningContent: reasoning,
		FinishReason:     "stop",
	}
}

// ArrangeGeminiOutputWithAllowedTools parses Gemini output and filters tool calls against allowedTools, falling back to text if none remain
func ArrangeGeminiOutputWithAllowedTools(raw string, allowedTools map[string]bool) *ArrangedOutput {
	res := ArrangeGeminiOutput(raw)
	if !res.IsToolCall || len(allowedTools) == 0 {
		return res
	}

	var valid []ToolCall
	for _, tc := range res.ToolCalls {
		if allowedTools[tc.Function.Name] {
			valid = append(valid, tc)
		}
	}

	if len(valid) > 0 {
		res.ToolCalls = valid
		return res
	}

	// No valid tool calls remain: fall back to clean text output
	res.ToolCalls = nil
	res.IsToolCall = false
	res.FinishReason = "stop"
	if res.CleanText == "" {
		_, textWithoutThoughts := extractThoughtBlocks(raw)
		res.CleanText = sanitizeGeminiContent(textWithoutThoughts)
	}
	return res
}

// ArrangeGeminiOutput analyzes, cleans, parses, and structures raw Gemini output
func ArrangeGeminiOutput(raw string) *ArrangedOutput {
	res := &ArrangedOutput{
		FinishReason: "stop",
	}

	// 1. Extract and separate Thinking / Reasoning blocks (<thought> or <thinking>)
	reasoning, text := extractThoughtBlocks(raw)
	res.ReasoningContent = reasoning

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
	if strings.Contains(text, `"name"`) && (strings.Contains(text, `"arguments"`) || strings.Contains(text, `"parameters"`)) {
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
