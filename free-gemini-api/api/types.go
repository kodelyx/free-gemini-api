package api

type OpenAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function OpenAIToolCallFunction `json:"function"`
}

type OpenAIChatMessage struct {
	Role             string           `json:"role"`
	Content          *string          `json:"content"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	Name             string           `json:"name,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type OpenAIChatCompletionRequest struct {
	Model      string              `json:"model"`
	Messages   []OpenAIChatMessage `json:"messages"`
	Tools      []map[string]any    `json:"tools,omitempty"`
	ToolChoice interface{}         `json:"tool_choice,omitempty"`
	Stream     bool                `json:"stream"`
	User       *string             `json:"user,omitempty"`
	Metadata   map[string]any      `json:"metadata,omitempty"`
}

type OpenAIChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIChatCompletionResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

type OpenAIDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
}

type OpenAIStreamChoice struct {
	Index        int         `json:"index"`
	Delta        OpenAIDelta `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type OpenAIChatCompletionChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
}

type ImageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	ResponseFormat string `json:"response_format"`
	UserID         string `json:"user_id"`
}

type ImageData struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

type ImageGenerationResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
}

// OpenAI Responses API (/v1/responses) types

type OpenAIResponseRequest struct {
	Model              string           `json:"model"`
	Input              interface{}      `json:"input"`
	Instructions       string           `json:"instructions,omitempty"`
	Stream             bool             `json:"stream"`
	Tools              []map[string]any `json:"tools,omitempty"`
	ToolChoice         interface{}      `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool            `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens    *int             `json:"max_output_tokens,omitempty"`
	Temperature        *float64         `json:"temperature,omitempty"`
	User               *string          `json:"user,omitempty"`
	Metadata           map[string]any   `json:"metadata,omitempty"`
	PreviousResponseID *string          `json:"previous_response_id,omitempty"`
	Store              *bool            `json:"store,omitempty"`
}

type OpenAIResponseOutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type OpenAIResponseOutputItem struct {
	ID        string                        `json:"id"`
	Type      string                        `json:"type"`
	Status    string                        `json:"status"`
	Role      string                        `json:"role,omitempty"`
	Content   []OpenAIResponseOutputContent `json:"content,omitempty"`
	CallID    string                        `json:"call_id,omitempty"`
	Name      string                        `json:"name,omitempty"`
	Arguments string                        `json:"arguments,omitempty"`
}

type OpenAIResponseOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type OpenAIResponseUsage struct {
	InputTokens         int                               `json:"input_tokens"`
	OutputTokens        int                               `json:"output_tokens"`
	TotalTokens         int                               `json:"total_tokens"`
	OutputTokensDetails OpenAIResponseOutputTokensDetails `json:"output_tokens_details"`
}

type OpenAIResponseObject struct {
	ID                 string                     `json:"id"`
	Object             string                     `json:"object"`
	CreatedAt          int64                      `json:"created_at"`
	Status             string                     `json:"status"`
	Background         bool                       `json:"background"`
	Error              interface{}                `json:"error"`
	IncompleteDetails  interface{}                `json:"incomplete_details"`
	Instructions       *string                    `json:"instructions"`
	MaxOutputTokens    *int                       `json:"max_output_tokens"`
	Model              string                     `json:"model"`
	Output             []OpenAIResponseOutputItem `json:"output"`
	OutputText         string                     `json:"output_text"`
	ParallelToolCalls  bool                       `json:"parallel_tool_calls"`
	PreviousResponseID *string                    `json:"previous_response_id"`
	Store              bool                       `json:"store"`
	Text               map[string]any             `json:"text"`
	Metadata           map[string]any             `json:"metadata"`
	Usage              *OpenAIResponseUsage       `json:"usage,omitempty"`
}
