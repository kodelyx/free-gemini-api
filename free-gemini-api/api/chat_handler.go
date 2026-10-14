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
	model := modelCfg.ID
	requiredTier := modelCfg.MinTier
	pool := GetWorkerPool()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if stream && len(images) == 0 {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		return c.SendStreamWriter(func(w *bufio.Writer) {
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
							streamResp.Images[i] = fmt.Sprintf("http://%s/output/%s", c.Host(), filename)
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
							streamResp.Videos[0] = fmt.Sprintf("http://%s/output/%s", c.Host(), filename)
						}
					}
				}
			}

			final, _ := json.Marshal(streamResp)
			fmt.Fprintf(w, "data: %s\n\n", final)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			w.Flush()

			userIP := c.IP()
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
	model := modelCfg.ID
	requiredTier := modelCfg.MinTier

	var prompt string
	hasToolResults := false
	var fullPromptBuilder strings.Builder

	for _, m := range req.Messages {
		contentStr := ""
		if m.Content != nil {
			contentStr = *m.Content
		}
		if m.Role == "user" {
			prompt = contentStr
			fullPromptBuilder.WriteString("User: " + contentStr + "\n")
		} else if m.Role == "assistant" {
			if len(m.ToolCalls) > 0 {
				tcJSON, _ := json.Marshal(m.ToolCalls)
				fullPromptBuilder.WriteString("Assistant called tools: " + string(tcJSON) + "\n")
			} else if contentStr != "" {
				fullPromptBuilder.WriteString("Assistant: " + contentStr + "\n")
			}
		} else if m.Role == "tool" {
			hasToolResults = true
			fullPromptBuilder.WriteString(fmt.Sprintf("[Tool Result for %s]: %s\n", m.Name, contentStr))
		} else if m.Role == "system" {
			fullPromptBuilder.WriteString("[System]: " + contentStr + "\n")
		}
	}

	if prompt == "" && len(req.Messages) > 0 {
		lastM := req.Messages[len(req.Messages)-1]
		if lastM.Content != nil {
			prompt = *lastM.Content
		}
	}

	// Inject Tool Instructions into conversation if tools are provided
	if len(req.Tools) > 0 {
		toolInstruction := gemini.FormatToolsInstruction(req.Tools)
		fullPromptBuilder.WriteString(toolInstruction)
	}

	// Multi-agent & session affinity resolution
	sessionID := strings.TrimSpace(c.Get("X-Agent-ID"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Get("X-Session-ID"))
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Get("X-Conversation-ID"))
	}
	if sessionID == "" && req.User != nil {
		sessionID = strings.TrimSpace(*req.User)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Query("session_id"))
	}
	if sessionID == "" {
		sessionID = c.IP()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	askPrompt := prompt
	if len(req.Messages) > 1 || hasToolResults || len(req.Tools) > 0 {
		if hasToolResults {
			fullPromptBuilder.WriteString("Assistant: ")
		}
		askPrompt = fullPromptBuilder.String()
	}

	pool := GetWorkerPool()

	if req.Stream {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		return c.SendStreamWriter(func(w *bufio.Writer) {
			chunkID := "chatcmpl-" + uuid.NewString()[:12]
			createdTime := time.Now().Unix()
			var usedWorker *AccountWorker
			var streamResp *gemini.GeminiResponse
			var accumulatedOutput strings.Builder

			streamErr := pool.ExecuteQueuedStreamWithTier(ctx, sessionID, requiredTier, func(wWorker *AccountWorker) error {
				usedWorker = wWorker
				sr, err := wWorker.Client.AskStreamModel(askPrompt, model, func(chunk string) {
					if len(req.Tools) > 0 {
						// Buffer chunks when tools are requested to evaluate tool call
						accumulatedOutput.WriteString(chunk)
						return
					}
					chunkResp := OpenAIChatCompletionChunk{
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
					}
					data, _ := json.Marshal(chunkResp)
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.Flush()
				})
				streamResp = sr
				return err
			})

			if streamErr != nil {
				log.Printf("❌ Streaming error: %v", streamErr)
				return
			}

			// If tools were provided, arrange and emit the structured result
			finishReason := "stop"
			var finalContentForLog string
			if len(req.Tools) > 0 {
				rawOutput := accumulatedOutput.String()
				if streamResp != nil && streamResp.Text != "" && len(rawOutput) < len(streamResp.Text) {
					rawOutput = streamResp.Text
				}
				arranged := gemini.ArrangeGeminiOutput(rawOutput)
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
					chunkResp := OpenAIChatCompletionChunk{
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
					}
					data, _ := json.Marshal(chunkResp)
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.Flush()
				} else if arranged.CleanText != "" {
					chunkResp := OpenAIChatCompletionChunk{
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
					}
					data, _ := json.Marshal(chunkResp)
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.Flush()
				}
			} else if streamResp != nil {
				finalContentForLog = streamResp.Text
			}

			finalResp := OpenAIChatCompletionChunk{
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
			}
			data, _ := json.Marshal(finalResp)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			w.Flush()

			userIP := c.IP()
			go func() {
				tier := "free"
				accID := ""
				if usedWorker != nil {
					tier = usedWorker.Tier
					accID = usedWorker.ID
				}
				pTokens := CountTokens(prompt)
				rTokens := CountTokens(finalContentForLog)
				convID := ""
				if streamResp != nil {
					convID = streamResp.ConversationID
				}
				db.LogMessageWithTier(convID, sessionID, "user", prompt, model, pTokens, tier, accID)
				if finalContentForLog != "" {
					db.LogMessageWithTier(convID, sessionID, "assistant", finalContentForLog, model, rTokens, tier, accID)
				}
				db.LogRequest("/v1/chat/completions", "POST", userIP, 200, float64(time.Now().Unix()-createdTime)*1000)
				AutoExportAnalytics()
			}()
		})
	}

	var usedWorker *AccountWorker
	resp, err := pool.ExecuteQueuedWithTier(ctx, sessionID, requiredTier, func(w *AccountWorker) (*gemini.GeminiResponse, error) {
		usedWorker = w
		return w.Client.AskModel(askPrompt, model)
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return c.Status(504).JSON(fiber.Map{"error": "Request timed out in worker queue."})
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

	arranged := gemini.ArrangeGeminiOutput(resp.Text)
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

	// Save to SQLite database asynchronously & auto-export Excel
	userIP := c.IP()
	go func() {
		tier := "free"
		accID := ""
		if usedWorker != nil {
			tier = usedWorker.Tier
			accID = usedWorker.ID
		}
		db.LogMessageWithTier(resp.ConversationID, sessionID, "user", prompt, model, pTokens, tier, accID)
		if arranged.CleanText != "" {
			db.LogMessageWithTier(resp.ConversationID, sessionID, "assistant", arranged.CleanText, model, cTokens, tier, accID)
		}
		db.LogRequest("/v1/chat/completions", "POST", userIP, 200, resp.Elapsed*1000)
		AutoExportAnalytics()
	}()

	return c.JSON(openAIResp)
}
