package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"goapi/db"
	"goapi/gemini"
	"io"
	"log"
	"mime/multipart"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)



// EstimateTokens calculates an accurate token count using word and character heuristics
func EstimateTokens(text string) int {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return 0
	}
	words := len(strings.Fields(clean))
	runes := len([]rune(clean))

	// Word-based heuristic: ~1.3 tokens per word (standard across English & Code)
	// Character-based fallback: max(1, (runes + 3) / 4)
	wTokens := int(float64(words) * 1.3)
	cTokens := (runes + 3) / 4

	tokens := wTokens
	if cTokens > tokens {
		tokens = cTokens
	}
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// HandleUnifiedChat handles /chat endpoint (text, images, screen recordings, ref videos)
func HandleUnifiedChat(c fiber.Ctx) error {
	var prompt, userID, reqModel string
	var newChat, stream bool
	var images []gemini.ImageInput

	contentType := string(c.Request().Header.ContentType())

	if strings.Contains(contentType, "multipart/form-data") {
		prompt = c.FormValue("prompt")
		userID = c.FormValue("user_id")
		newChat = c.FormValue("new_chat") == "true"
		stream = c.FormValue("stream") == "true"
		reqModel = c.FormValue("model")

		form, err := c.MultipartForm()
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid multipart form"})
		}

		var fileHeaders []*multipart.FileHeader
		if fhs, ok := form.File["images"]; ok {
			fileHeaders = append(fileHeaders, fhs...)
		}
		if fhs, ok := form.File["image"]; ok {
			fileHeaders = append(fileHeaders, fhs...)
		}

		for _, fh := range fileHeaders {
			file, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				continue
			}

			mime := fh.Header.Get("Content-Type")
			if mime == "" {
				ext := strings.ToLower(fh.Filename)
				switch {
				case strings.HasSuffix(ext, ".png"):
					mime = "image/png"
				case strings.HasSuffix(ext, ".webp"):
					mime = "image/webp"
				case strings.HasSuffix(ext, ".gif"):
					mime = "image/gif"
				case strings.HasSuffix(ext, ".mp4"):
					mime = "video/mp4"
				default:
					mime = "image/jpeg"
				}
			}
			images = append(images, gemini.ImageInput{Data: data, Filename: fh.Filename, MimeType: mime})
		}
	} else {
		var req gemini.ChatRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
		}
		prompt = req.Prompt
		if prompt == "" {
			prompt = req.Message
		}
		userID = req.UserID
		newChat = req.NewChat
		stream = req.Stream
		reqModel = req.Model
	}

	sessionID := userID
	if sessionID == "" {
		sessionID = c.IP()
	}

	if newChat {
		UserSessions.Delete(sessionID)
	}

	modelCfg := gemini.GetModelRegistry().Resolve(reqModel)
	modelCfg = applyTaskModeOverride(c, reqModel, modelCfg)
	model := modelCfg.ID
	requiredTier := modelCfg.MinTier
	pool := GetWorkerPool()

	if stream && len(images) == 0 {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		hostStr := c.Host()
		userIP := c.IP()

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			var usedWorker *AccountWorker
			var streamResp *gemini.GeminiResponse
			startTime := time.Now()

			streamErr := pool.ExecuteQueuedStreamWithTier(ctx, sessionID, requiredTier, func(worker *AccountWorker) error {
				usedWorker = worker
				sr, err := worker.Client.AskStreamModel(prompt, model, func(chunk string) {
					data, _ := json.Marshal(fiber.Map{"text": chunk})
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.Flush()
				})
				streamResp = sr
				return err
			})

			if streamErr != nil {
				data, _ := json.Marshal(fiber.Map{"error": streamErr.Error()})
				fmt.Fprintf(w, "data: %s\n\n", data)
				w.Flush()
				return
			}

			if streamResp != nil && usedWorker != nil {
				if len(streamResp.Images) > 0 {
					for i, imgURL := range streamResp.Images {
						filename := fmt.Sprintf("img_%s_%d.png", streamResp.ResponseID, i)
						if streamResp.ResponseID == "" {
							filename = fmt.Sprintf("img_%d_%d.png", time.Now().Unix(), i)
						}
						if err := DownloadAndClean(usedWorker.Client, imgURL, filename, "image"); err == nil {
							streamResp.Images[i] = fmt.Sprintf("http://%s/output/%s", hostStr, filename)
						}
					}
				}

				if len(streamResp.Videos) > 0 {
					vidURL := streamResp.Videos[0]
					if vidURL != "" && len(vidURL) > 50 {
						filename := fmt.Sprintf("vid_%s_0.mp4", streamResp.ResponseID)
						if streamResp.ResponseID == "" {
							filename = fmt.Sprintf("vid_%d_0.mp4", time.Now().Unix())
						}
						if err := DownloadAndClean(usedWorker.Client, vidURL, filename, "video"); err == nil {
							streamResp.Videos[0] = fmt.Sprintf("http://%s/output/%s", hostStr, filename)
						}
					}
				}
			}

			final, _ := json.Marshal(streamResp)
			fmt.Fprintf(w, "data: %s\n\n", final)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			w.Flush()

			elapsed := time.Since(startTime).Seconds()
			go func() {
				tier := "free"
				accID := ""
				if usedWorker != nil {
					tier = usedWorker.Tier
					accID = usedWorker.ID
				}
				pTokens := CountTokens(prompt)
				rTokens := 0
				convID := ""
				if streamResp != nil {
					rTokens = CountTokens(streamResp.Text)
					convID = streamResp.ConversationID
				}
				db.LogMessageWithTier(convID, sessionID, "user", prompt, model, pTokens, tier, accID)
				if streamResp != nil && streamResp.Text != "" {
					db.LogMessageWithTier(convID, sessionID, "assistant", streamResp.Text, model, rTokens, tier, accID)
				}
				db.LogRequest("/chat", "POST", userIP, 200, elapsed*1000)
				AutoExportAnalytics()
			}()
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if len(images) > 0 {
		log.Printf("📸 Image/Media request: %d items, prompt: %s (model: %s, tier: %s)", len(images), prompt, model, requiredTier)
	}

	var usedWorker *AccountWorker
	resp, err := pool.ExecuteQueuedWithTier(ctx, sessionID, requiredTier, func(w *AccountWorker) (*gemini.GeminiResponse, error) {
		usedWorker = w
		if len(images) > 0 {
			return w.Client.AskWithImagesModel(prompt, images, model)
		}
		return w.Client.AskModel(prompt, model)
	})

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			UserSessions.Delete(sessionID)
			return c.Status(504).JSON(fiber.Map{"error": "Request timed out. Session reset."})
		}
		if strings.Contains(strings.ToLower(err.Error()), "rate_limit") || strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "cooldown") {
			return c.Status(429).JSON(fiber.Map{
				"error": fiber.Map{
					"message": err.Error(),
					"type":    "rate_limit_error",
					"code":    429,
				},
			})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	if len(resp.Images) > 0 && usedWorker != nil {
		for i, imgURL := range resp.Images {
			filename := fmt.Sprintf("img_%s_%d.png", resp.ResponseID, i)
			if resp.ResponseID == "" {
				filename = fmt.Sprintf("img_%d_%d.png", time.Now().Unix(), i)
			}
			if err := DownloadAndClean(usedWorker.Client, imgURL, filename, "image"); err == nil {
				resp.Images[i] = fmt.Sprintf("http://%s/output/%s", c.Host(), filename)
			}
		}
	}

	if len(resp.Videos) > 0 && usedWorker != nil {
		vidURL := resp.Videos[0]
		if vidURL != "" && len(vidURL) > 50 {
			filename := fmt.Sprintf("vid_%s_0.mp4", resp.ResponseID)
			if resp.ResponseID == "" {
				filename = fmt.Sprintf("vid_%d_0.mp4", time.Now().Unix())
			}
			if err := DownloadAndClean(usedWorker.Client, vidURL, filename, "video"); err == nil {
				resp.Videos[0] = fmt.Sprintf("http://%s/output/%s", c.Host(), filename)
			}
		}
	}

	// Save to SQLite database asynchronously & auto-export Excel
	userIP := c.IP()
	hostStr := c.Host()
	go func() {
		tier := "free"
		accID := ""
		if usedWorker != nil {
			tier = usedWorker.Tier
			accID = usedWorker.ID
		}
		pTokens := CountTokens(prompt)
		rTokens := CountTokens(resp.Text)
		db.LogMessageWithTier(resp.ConversationID, sessionID, "user", prompt, model, pTokens, tier, accID)
		if resp.Text != "" {
			db.LogMessageWithTier(resp.ConversationID, sessionID, "assistant", resp.Text, model, rTokens, tier, accID)
		}
		for _, imgURL := range resp.Images {
			fName := filepath.Base(imgURL)
			db.LogMediaGeneration("image", prompt, fName, "./output/"+fName, fmt.Sprintf("http://%s/output/%s", hostStr, fName), "16:9", resp.ResponseID)
		}
		for _, vidURL := range resp.Videos {
			fName := filepath.Base(vidURL)
			db.LogMediaGeneration("video", prompt, fName, "./output/"+fName, fmt.Sprintf("http://%s/output/%s", hostStr, fName), "16:9", resp.ResponseID)
		}
		db.LogRequest("/chat", "POST", userIP, 200, resp.Elapsed*1000)
		AutoExportAnalytics()
	}()

	return c.JSON(resp)
}

// startSSEHeartbeat emits `: ping\n\n` every 15 seconds while the stream is open and stops cleanly on defer.
func startSSEHeartbeat(w *bufio.Writer, writeMu *sync.Mutex) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	var once sync.Once

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				writeMu.Lock()
				_, writeErr := fmt.Fprintf(w, ": ping\n\n")
				if writeErr == nil {
					writeErr = w.Flush()
				}
				writeMu.Unlock()
				if writeErr != nil {
					return
				}
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

func isToolChoiceNone(toolChoice interface{}) bool {
	s, ok := toolChoice.(string)
	return ok && strings.EqualFold(strings.TrimSpace(s), "none")
}

func buildAllowedToolNames(tools []map[string]any) map[string]bool {
	allowed := make(map[string]bool)
	for _, t := range tools {
		if fn, ok := t["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && strings.TrimSpace(name) != "" {
				allowed[strings.TrimSpace(name)] = true
			}
		} else if name, ok := t["name"].(string); ok && strings.TrimSpace(name) != "" {
			allowed[strings.TrimSpace(name)] = true
		}
	}
	return allowed
}

func resolveOpenAISessionID(c fiber.Ctx, user *string, metadata map[string]any) string {
	candidates := []string{
		c.Get("X-Agent-ID"),
		c.Get("X-Session-ID"),
		c.Get("X-Conversation-ID"),
	}
	if user != nil {
		candidates = append(candidates, *user)
	}
	for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId"} {
		if metadata != nil {
			if val, ok := metadata[key].(string); ok {
				candidates = append(candidates, val)
			}
		}
	}
	candidates = append(candidates, c.Query("session_id"), c.IP())

	for _, cand := range candidates {
		if trimmed := strings.TrimSpace(cand); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func prepareOpenAIPrompt(messages []OpenAIChatMessage, tools []map[string]any, toolChoice interface{}) (prompt string, askPrompt string, toolsActive bool, allowedTools map[string]bool) {
	hasToolResults := false
	var fullPromptBuilder strings.Builder

	for _, m := range messages {
		contentStr := ""
		if m.Content != nil {
			contentStr = *m.Content
		}
		switch m.Role {
		case "user":
			prompt = contentStr
			fullPromptBuilder.WriteString("User: " + contentStr + "\n")
		case "assistant":
			if len(m.ToolCalls) > 0 {
				tcJSON, _ := json.Marshal(m.ToolCalls)
				fullPromptBuilder.WriteString("Assistant called tools: " + string(tcJSON) + "\n")
			} else if contentStr != "" {
				fullPromptBuilder.WriteString("Assistant: " + contentStr + "\n")
			}
		case "tool":
			hasToolResults = true
			toolLabel := m.Name
			if toolLabel == "" {
				toolLabel = m.ToolCallID
			}
			fullPromptBuilder.WriteString(fmt.Sprintf("[Tool Result for %s]: %s\n", toolLabel, contentStr))
		case "system", "developer":
			fullPromptBuilder.WriteString("[System]: " + contentStr + "\n")
		}
	}

	if prompt == "" && len(messages) > 0 {
		lastM := messages[len(messages)-1]
		if lastM.Content != nil {
			prompt = *lastM.Content
		}
	}

	toolsActive = len(tools) > 0 && !isToolChoiceNone(toolChoice)
	if toolsActive {
		allowedTools = buildAllowedToolNames(tools)
		toolInstruction := gemini.FormatToolsInstructionWithChoice(tools, toolChoice)
		if toolInstruction != "" {
			fullPromptBuilder.WriteString(toolInstruction)
		}
	}

	askPrompt = prompt
	if len(messages) > 1 || hasToolResults || toolsActive || askPrompt == "" {
		if hasToolResults {
			fullPromptBuilder.WriteString("Assistant: ")
		}
		askPrompt = fullPromptBuilder.String()
	}

	return prompt, askPrompt, toolsActive, allowedTools
}

func arrangeModelOutput(raw string, toolsActive bool, allowedTools map[string]bool) *gemini.ArrangedOutput {
	if toolsActive {
		return gemini.ArrangeGeminiOutputWithAllowedTools(raw, allowedTools)
	}
	return gemini.ArrangeGeminiOutputTextOnly(raw)
}

func mapGeminiError(c fiber.Ctx, ctx context.Context, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return c.Status(504).JSON(fiber.Map{"error": "Request timed out in worker queue."})
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "rate_limit") || strings.Contains(err.Error(), "429") || strings.Contains(lower, "cooldown") {
		return c.Status(429).JSON(fiber.Map{
			"error": fiber.Map{
				"message": err.Error(),
				"type":    "rate_limit_error",
				"code":    429,
			},
		})
	}
	return c.Status(500).JSON(fiber.Map{"error": err.Error()})
}

func setSSEHeaders(c fiber.Ctx) {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
}

func logOpenAIAsync(endpoint, convID, sessionID, userPrompt, assistantText, model string, pTokens, rTokens int, userIP string, elapsedMs float64, usedWorker *AccountWorker) {
	go func() {
		tier := "free"
		accID := ""
		if usedWorker != nil {
			tier = usedWorker.Tier
			accID = usedWorker.ID
		}
		db.LogMessageWithTier(convID, sessionID, "user", userPrompt, model, pTokens, tier, accID)
		if assistantText != "" {
			db.LogMessageWithTier(convID, sessionID, "assistant", assistantText, model, rTokens, tier, accID)
		}
		db.LogRequest(endpoint, "POST", userIP, 200, elapsedMs)
		AutoExportAnalytics()
	}()
}

func applyTaskModeOverride(c fiber.Ctx, reqModel string, modelCfg *gemini.ModelConfig) *gemini.ModelConfig {
	if modelCfg != nil && (strings.EqualFold(c.Get("X-Gemini-Mode"), "task") || strings.EqualFold(c.Query("mode"), "task") || strings.HasSuffix(strings.ToLower(reqModel), "-task")) {
		cfgCopy := *modelCfg
		cfgCopy.AppMode = 40
		return &cfgCopy
	}
	return modelCfg
}

// HandleOpenAIChatCompletions handles /v1/chat/completions with Native Tool Calling & Output Arranger
func HandleOpenAIChatCompletions(c fiber.Ctx) error {
	var req OpenAIChatCompletionRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
	}

	if len(req.Messages) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Messages array is empty"})
	}

	modelCfg := gemini.GetModelRegistry().Resolve(req.Model)
	modelCfg = applyTaskModeOverride(c, req.Model, modelCfg)
	model := modelCfg.ID
	requiredTier := modelCfg.MinTier

	prompt, askPrompt, toolsActive, allowedTools := prepareOpenAIPrompt(req.Messages, req.Tools, req.ToolChoice)
	sessionID := resolveOpenAISessionID(c, req.User, req.Metadata)
	pool := GetWorkerPool()

	if req.Stream {
		setSSEHeaders(c)
		userIP := c.IP()

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			var writeMu sync.Mutex
			stopHeartbeat := startSSEHeartbeat(w, &writeMu)
			defer stopHeartbeat()

			chunkID := "chatcmpl-" + uuid.NewString()[:12]
			createdTime := time.Now().Unix()
			var usedWorker *AccountWorker
			var streamResp *gemini.GeminiResponse
			var accumulatedOutput strings.Builder

			writeJSONLine := func(payload interface{}) {
				data, _ := json.Marshal(payload)
				writeMu.Lock()
				fmt.Fprintf(w, "data: %s\n\n", data)
				w.Flush()
				writeMu.Unlock()
			}

			streamErr := pool.ExecuteQueuedStreamWithTier(ctx, sessionID, requiredTier, func(wWorker *AccountWorker) error {
				usedWorker = wWorker
				sr, err := wWorker.Client.AskStreamModel(askPrompt, model, func(chunk string) {
					if toolsActive {
						// Buffer chunks when tools are active to evaluate tool call
						accumulatedOutput.WriteString(chunk)
						return
					}
					writeJSONLine(OpenAIChatCompletionChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: createdTime,
						Model:   model,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									Content: chunk,
								},
							},
						},
					})
				})
				streamResp = sr
				return err
			})

			if streamErr != nil {
				log.Printf("❌ Streaming error: %v", streamErr)
				writeMu.Lock()
				fmt.Fprintf(w, "data: [DONE]\n\n")
				w.Flush()
				writeMu.Unlock()
				return
			}

			// If tools were active, arrange and emit the structured result
			finishReason := "stop"
			var finalContentForLog string
			if toolsActive {
				rawOutput := accumulatedOutput.String()
				if streamResp != nil && streamResp.Text != "" && len(rawOutput) < len(streamResp.Text) {
					rawOutput = streamResp.Text
				}
				arranged := arrangeModelOutput(rawOutput, true, allowedTools)
				finishReason = arranged.FinishReason
				finalContentForLog = arranged.CleanText

				if arranged.IsToolCall {
					var toolCalls []OpenAIToolCall
					for _, tc := range arranged.ToolCalls {
						toolCalls = append(toolCalls, OpenAIToolCall{
							ID:   tc.ID,
							Type: tc.Type,
							Function: OpenAIToolCallFunction{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						})
					}
					writeJSONLine(OpenAIChatCompletionChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: createdTime,
						Model:   model,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									Role:      "assistant",
									ToolCalls: toolCalls,
								},
							},
						},
					})
				} else if arranged.CleanText != "" {
					writeJSONLine(OpenAIChatCompletionChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: createdTime,
						Model:   model,
						Choices: []OpenAIStreamChoice{
							{
								Index: 0,
								Delta: OpenAIDelta{
									Role:             "assistant",
									Content:          arranged.CleanText,
									ReasoningContent: arranged.ReasoningContent,
								},
							},
						},
					})
				}
			} else if streamResp != nil {
				finalContentForLog = streamResp.Text
			}

			writeJSONLine(OpenAIChatCompletionChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: createdTime,
				Model:   model,
				Choices: []OpenAIStreamChoice{
					{
						Index:        0,
						FinishReason: finishReason,
					},
				},
			})

			writeMu.Lock()
			fmt.Fprintf(w, "data: [DONE]\n\n")
			w.Flush()
			writeMu.Unlock()

			convID := ""
			if streamResp != nil {
				convID = streamResp.ConversationID
			}
			logOpenAIAsync("/v1/chat/completions", convID, sessionID, prompt, finalContentForLog, model, CountTokens(prompt), CountTokens(finalContentForLog), userIP, float64(time.Now().Unix()-createdTime)*1000, usedWorker)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var usedWorker *AccountWorker
	resp, err := pool.ExecuteQueuedWithTier(ctx, sessionID, requiredTier, func(w *AccountWorker) (*gemini.GeminiResponse, error) {
		usedWorker = w
		return w.Client.AskModel(askPrompt, model)
	})
	if err != nil {
		return mapGeminiError(c, ctx, err)
	}

	arranged := arrangeModelOutput(resp.Text, toolsActive, allowedTools)
	pTokens := CountTokens(prompt)
	cTokens := CountTokens(arranged.CleanText)

	var message OpenAIChatMessage
	if arranged.IsToolCall {
		var toolCalls []OpenAIToolCall
		for _, tc := range arranged.ToolCalls {
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: OpenAIToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		message = OpenAIChatMessage{
			Role:      "assistant",
			Content:   nil,
			ToolCalls: toolCalls,
		}
		if arranged.ReasoningContent != "" {
			rc := arranged.ReasoningContent
			message.ReasoningContent = &rc
		}
	} else {
		cleanText := arranged.CleanText
		message = OpenAIChatMessage{
			Role:    "assistant",
			Content: &cleanText,
		}
		if arranged.ReasoningContent != "" {
			rc := arranged.ReasoningContent
			message.ReasoningContent = &rc
		}
	}

	openAIResp := OpenAIChatCompletionResponse{
		ID:      "chatcmpl-" + uuid.NewString()[:12],
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChoice{
			{
				Index:        0,
				Message:      message,
				FinishReason: arranged.FinishReason,
			},
		},
		Usage: &OpenAIUsage{
			PromptTokens:     pTokens,
			CompletionTokens: cTokens,
			TotalTokens:      pTokens + cTokens,
		},
	}

	logOpenAIAsync("/v1/chat/completions", resp.ConversationID, sessionID, prompt, arranged.CleanText, model, pTokens, cTokens, c.IP(), resp.Elapsed*1000, usedWorker)

	return c.JSON(openAIResp)
}

func extractResponsesContentText(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []interface{}:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			partType, _ := m["type"].(string)
			if partType == "" || partType == "input_text" || partType == "output_text" || partType == "text" {
				if txt, ok := m["text"].(string); ok && strings.TrimSpace(txt) != "" {
					parts = append(parts, strings.TrimSpace(txt))
				} else if txt, ok := m["input_text"].(string); ok && strings.TrimSpace(txt) != "" {
					parts = append(parts, strings.TrimSpace(txt))
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func stringifyResponseValue(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		if text := extractResponsesContentText(raw); text != "" {
			return text
		}
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func convertResponsesInputToMessages(req OpenAIResponseRequest) ([]OpenAIChatMessage, error) {
	var messages []OpenAIChatMessage

	if instr := strings.TrimSpace(req.Instructions); instr != "" {
		messages = append(messages, OpenAIChatMessage{
			Role:    "system",
			Content: &instr,
		})
	}

	switch inp := req.Input.(type) {
	case string:
		if trimmed := strings.TrimSpace(inp); trimmed != "" {
			messages = append(messages, OpenAIChatMessage{
				Role:    "user",
				Content: &trimmed,
			})
		}
	case []interface{}:
		for _, item := range inp {
			if strItem, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(strItem); trimmed != "" {
					messages = append(messages, OpenAIChatMessage{
						Role:    "user",
						Content: &trimmed,
					})
				}
				continue
			}

			record, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			itemType, _ := record["type"].(string)
			switch itemType {
			case "function_call":
				callID, _ := record["call_id"].(string)
				if callID == "" {
					callID, _ = record["id"].(string)
				}
				name, _ := record["name"].(string)
				args := stringifyResponseValue(record["arguments"])
				if args == "" {
					args = "{}"
				}
				if name != "" {
					messages = append(messages, OpenAIChatMessage{
						Role: "assistant",
						ToolCalls: []OpenAIToolCall{
							{
								ID:   callID,
								Type: "function",
								Function: OpenAIToolCallFunction{
									Name:      name,
									Arguments: args,
								},
							},
						},
					})
				}
			case "function_call_output":
				callID, _ := record["call_id"].(string)
				out := stringifyResponseValue(record["output"])
				messages = append(messages, OpenAIChatMessage{
					Role:       "tool",
					ToolCallID: callID,
					Name:       callID,
					Content:    &out,
				})
			case "", "message":
				role, _ := record["role"].(string)
				role = strings.TrimSpace(role)
				if role == "" {
					role = "user"
				}
				content := extractResponsesContentText(record["content"])
				if content == "" {
					continue
				}
				name, _ := record["name"].(string)
				messages = append(messages, OpenAIChatMessage{
					Role:    role,
					Content: &content,
					Name:    name,
				})
			}
		}
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("input must contain at least one message or item")
	}

	return messages, nil
}

func buildOpenAIResponsePayload(
	respID string,
	msgID string,
	model string,
	createdAt int64,
	status string,
	req OpenAIResponseRequest,
	outputItems []OpenAIResponseOutputItem,
	outputText string,
	usage *OpenAIResponseUsage,
) OpenAIResponseObject {
	var instrPtr *string
	if strings.TrimSpace(req.Instructions) != "" {
		trimmed := strings.TrimSpace(req.Instructions)
		instrPtr = &trimmed
	}
	parallel := true
	if req.ParallelToolCalls != nil {
		parallel = *req.ParallelToolCalls
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if outputItems == nil {
		outputItems = []OpenAIResponseOutputItem{}
	}

	return OpenAIResponseObject{
		ID:                 respID,
		Object:             "response",
		CreatedAt:          createdAt,
		Status:             status,
		Background:         false,
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       instrPtr,
		MaxOutputTokens:    req.MaxOutputTokens,
		Model:              model,
		Output:             outputItems,
		OutputText:         outputText,
		ParallelToolCalls:  parallel,
		PreviousResponseID: req.PreviousResponseID,
		Store:              false,
		Text: map[string]any{
			"format": map[string]any{"type": "text"},
		},
		Metadata: metadata,
		Usage:    usage,
	}
}

func buildResponsesOutputAndText(arranged *gemini.ArrangedOutput, msgID string) ([]OpenAIResponseOutputItem, string) {
	if arranged.IsToolCall && len(arranged.ToolCalls) > 0 {
		items := make([]OpenAIResponseOutputItem, 0, len(arranged.ToolCalls))
		for _, tc := range arranged.ToolCalls {
			items = append(items, OpenAIResponseOutputItem{
				ID:        "fc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
				Type:      "function_call",
				Status:    "completed",
				CallID:    tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		return items, ""
	}

	items := []OpenAIResponseOutputItem{
		{
			ID:     msgID,
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []OpenAIResponseOutputContent{
				{
					Type:        "output_text",
					Text:        arranged.CleanText,
					Annotations: []any{},
				},
			},
		},
	}
	return items, arranged.CleanText
}

// HandleOpenAIResponses handles POST /v1/responses (OpenAI Responses API for Codex CLI / agents)
func HandleOpenAIResponses(c fiber.Ctx) error {
	var req OpenAIResponseRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
	}

	messages, err := convertResponsesInputToMessages(req)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	modelCfg := gemini.GetModelRegistry().Resolve(req.Model)
	modelCfg = applyTaskModeOverride(c, req.Model, modelCfg)
	model := modelCfg.ID
	requiredTier := modelCfg.MinTier

	prompt, askPrompt, toolsActive, allowedTools := prepareOpenAIPrompt(messages, req.Tools, req.ToolChoice)
	logPrompt := prompt
	if logPrompt == "" {
		logPrompt = askPrompt
	}
	sessionID := resolveOpenAISessionID(c, req.User, req.Metadata)
	pool := GetWorkerPool()

	respID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	createdAt := time.Now().Unix()

	if req.Stream {
		setSSEHeaders(c)
		userIP := c.IP()

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			var writeMu sync.Mutex
			stopHeartbeat := startSSEHeartbeat(w, &writeMu)
			defer stopHeartbeat()

			seq := 0
			writeEvent := func(eventName string, payload interface{}) {
				data, _ := json.Marshal(payload)
				writeMu.Lock()
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, data)
				w.Flush()
				writeMu.Unlock()
			}

			seq++
			createdObj := buildOpenAIResponsePayload(respID, msgID, model, createdAt, "in_progress", req, nil, "", nil)
			writeEvent("response.created", map[string]any{
				"type":            "response.created",
				"sequence_number": seq,
				"response":        createdObj,
			})

			var usedWorker *AccountWorker
			var streamResp *gemini.GeminiResponse
			var accumulatedOutput strings.Builder

			streamErr := pool.ExecuteQueuedStreamWithTier(ctx, sessionID, requiredTier, func(wWorker *AccountWorker) error {
				usedWorker = wWorker
				sr, err := wWorker.Client.AskStreamModel(askPrompt, model, func(chunk string) {
					if toolsActive {
						accumulatedOutput.WriteString(chunk)
						return
					}
					seq++
					writeEvent("response.output_text.delta", map[string]any{
						"type":            "response.output_text.delta",
						"sequence_number": seq,
						"response_id":     respID,
						"item_id":         msgID,
						"output_index":    0,
						"content_index":   0,
						"delta":           chunk,
					})
				})
				streamResp = sr
				return err
			})

			if streamErr != nil {
				log.Printf("❌ Responses streaming error: %v", streamErr)
				seq++
				writeEvent("error", map[string]any{
					"type":            "error",
					"sequence_number": seq,
					"message":         streamErr.Error(),
				})
				writeMu.Lock()
				fmt.Fprintf(w, "data: [DONE]\n\n")
				w.Flush()
				writeMu.Unlock()
				return
			}

			rawOutput := accumulatedOutput.String()
			if streamResp != nil && streamResp.Text != "" && len(rawOutput) < len(streamResp.Text) {
				rawOutput = streamResp.Text
			}

			var outputItems []OpenAIResponseOutputItem
			var outputText string
			var completionTokens int

			if toolsActive {
				arranged := arrangeModelOutput(rawOutput, true, allowedTools)
				outputItems, outputText = buildResponsesOutputAndText(arranged, msgID)
				if arranged.IsToolCall {
					for idx, item := range outputItems {
						inProgressItem := OpenAIResponseOutputItem{
							ID:        item.ID,
							Type:      "function_call",
							Status:    "in_progress",
							CallID:    item.CallID,
							Name:      item.Name,
							Arguments: "",
						}
						seq++
						writeEvent("response.output_item.added", map[string]any{
							"type":            "response.output_item.added",
							"sequence_number": seq,
							"response_id":     respID,
							"output_index":    idx,
							"item":            inProgressItem,
						})
						seq++
						writeEvent("response.function_call_arguments.delta", map[string]any{
							"type":            "response.function_call_arguments.delta",
							"sequence_number": seq,
							"response_id":     respID,
							"item_id":         item.ID,
							"output_index":    idx,
							"delta":           item.Arguments,
						})
						seq++
						writeEvent("response.function_call_arguments.done", map[string]any{
							"type":            "response.function_call_arguments.done",
							"sequence_number": seq,
							"response_id":     respID,
							"item_id":         item.ID,
							"output_index":    idx,
							"arguments":       item.Arguments,
						})
						seq++
						writeEvent("response.output_item.done", map[string]any{
							"type":            "response.output_item.done",
							"sequence_number": seq,
							"response_id":     respID,
							"output_index":    idx,
							"item":            item,
						})
						completionTokens += CountTokens(item.Arguments)
					}
				} else if outputText != "" {
					seq++
					writeEvent("response.output_text.delta", map[string]any{
						"type":            "response.output_text.delta",
						"sequence_number": seq,
						"response_id":     respID,
						"item_id":         msgID,
						"output_index":    0,
						"content_index":   0,
						"delta":           outputText,
					})
					completionTokens = CountTokens(outputText)
				}
			} else {
				outputText = rawOutput
				outputItems = []OpenAIResponseOutputItem{
					{
						ID:     msgID,
						Type:   "message",
						Status: "completed",
						Role:   "assistant",
						Content: []OpenAIResponseOutputContent{
							{
								Type:        "output_text",
								Text:        outputText,
								Annotations: []any{},
							},
						},
					},
				}
				completionTokens = CountTokens(outputText)
			}

			pTokens := CountTokens(logPrompt)
			usage := &OpenAIResponseUsage{
				InputTokens:  pTokens,
				OutputTokens: completionTokens,
				TotalTokens:  pTokens + completionTokens,
			}

			seq++
			completedObj := buildOpenAIResponsePayload(respID, msgID, model, createdAt, "completed", req, outputItems, outputText, usage)
			writeEvent("response.completed", map[string]any{
				"type":            "response.completed",
				"sequence_number": seq,
				"response":        completedObj,
			})

			writeMu.Lock()
			fmt.Fprintf(w, "data: [DONE]\n\n")
			w.Flush()
			writeMu.Unlock()

			convID := ""
			if streamResp != nil {
				convID = streamResp.ConversationID
			}
			logOpenAIAsync("/v1/responses", convID, sessionID, logPrompt, outputText, model, pTokens, completionTokens, userIP, float64(time.Now().Unix()-createdAt)*1000, usedWorker)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var usedWorker *AccountWorker
	resp, err := pool.ExecuteQueuedWithTier(ctx, sessionID, requiredTier, func(w *AccountWorker) (*gemini.GeminiResponse, error) {
		usedWorker = w
		return w.Client.AskModel(askPrompt, model)
	})
	if err != nil {
		return mapGeminiError(c, ctx, err)
	}

	arranged := arrangeModelOutput(resp.Text, toolsActive, allowedTools)
	outputItems, outputText := buildResponsesOutputAndText(arranged, msgID)

	pTokens := CountTokens(logPrompt)
	cTokens := CountTokens(outputText)
	if arranged.IsToolCall {
		tcBytes, _ := json.Marshal(arranged.ToolCalls)
		cTokens = CountTokens(string(tcBytes))
	}

	usage := &OpenAIResponseUsage{
		InputTokens:  pTokens,
		OutputTokens: cTokens,
		TotalTokens:  pTokens + cTokens,
	}

	respObj := buildOpenAIResponsePayload(respID, msgID, model, createdAt, "completed", req, outputItems, outputText, usage)
	logOpenAIAsync("/v1/responses", resp.ConversationID, sessionID, logPrompt, outputText, model, pTokens, cTokens, c.IP(), resp.Elapsed*1000, usedWorker)

	return c.JSON(respObj)
}
