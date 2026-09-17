package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ─── JSON-RPC 2.0 Protocol Types ──────────────────────────────────────────────

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

// ─── MCP Tool Types ───────────────────────────────────────────────────────────

type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type PromptArg struct {
	Prompt         string `json:"prompt"`
	RefImagePath   string `json:"ref_image_path,omitempty"`
	RefVideoPath   string `json:"ref_video_path,omitempty"`
	StartImagePath string `json:"start_image_path,omitempty"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type CallToolResult struct {
	Content []TextContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ─── Configuration ────────────────────────────────────────────────────────────

const (
	mcpServerName    = "free-gemini-mcp"
	mcpServerVersion = "2.1.0"

	mcpMaxRetries    = 3
	mcpBaseRetryWait = 2 * time.Second
	mcpHttpTimeout   = 5 * time.Minute

	mcpMaxScannerBuffer = 10 * 1024 * 1024
)

func getMCPBackendURL() string {
	if u := os.Getenv("GEMINI_API_URL"); u != "" {
		return u
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8001"
	}
	return "http://localhost:" + port
}

// RunMCPServer starts the stdio MCP server loop
func RunMCPServer() {
	log.SetOutput(os.Stderr)
	backendURL := getMCPBackendURL()
	log.Printf("🔌 %s v%s starting over stdio...", mcpServerName, mcpServerVersion)
	log.Printf("🔗 Backend API: %s", backendURL)

	go checkMCPBackendHealth(backendURL)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, mcpMaxScannerBuffer), mcpMaxScannerBuffer)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("⚠️ Failed to parse JSON-RPC request: %v", err)
			sendMCPError(nil, -32700, "Parse error: invalid JSON", nil)
			continue
		}

		handleMCPRequest(req, backendURL)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("❌ Scanner error: %v", err)
	}
}

func checkMCPBackendHealth(backendURL string) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(backendURL + "/health")
	if err != nil {
		log.Printf("⚠️ Backend API health check failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("✅ Backend API is reachable (status: %d)", resp.StatusCode)
}

func handleMCPRequest(req JSONRPCRequest, backendURL string) {
	switch req.Method {

	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]string{
				"name":    mcpServerName,
				"version": mcpServerVersion,
			},
		}
		sendMCPResult(req.ID, result)

	case "notifications/initialized":
		log.Println("✅ Client initialized successfully")

	case "ping":
		sendMCPResult(req.ID, map[string]interface{}{})

	case "tools/list":
		sendMCPResult(req.ID, getMCPToolDefinitions())

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendMCPError(req.ID, -32602, "Invalid params: could not parse tool call parameters", nil)
			return
		}

		var args PromptArg
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			sendMCPError(req.ID, -32602, "Invalid arguments: could not parse tool arguments", nil)
			return
		}

		if args.Prompt == "" && params.Name != "reset_session" && params.Name != "check_health" {
			sendMCPResult(req.ID, &CallToolResult{
				Content: []TextContent{{Type: "text", Text: "❌ Error: 'prompt' is required but was empty."}},
				IsError: true,
			})
			return
		}

		result, err := callMCPTool(params.Name, args, backendURL)
		if err != nil {
			sendMCPResult(req.ID, &CallToolResult{
				Content: []TextContent{{Type: "text", Text: formatMCPToolError(err)}},
				IsError: true,
			})
			return
		}

		sendMCPResult(req.ID, result)

	default:
		if req.ID == nil {
			log.Printf("📩 Ignoring unknown notification: %s", req.Method)
			return
		}
		sendMCPError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method), nil)
	}
}

func getMCPToolDefinitions() map[string]interface{} {
	return map[string]interface{}{
		"tools": []map[string]interface{}{
			{
				"name":        "generate_image",
				"description": "Generate a high-quality image using Gemini Image with auto-cleaned watermarks. Supports optional Image-to-Image (I2I).",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{
							"type":        "string",
							"description": "Detailed text description of the image to generate.",
						},
						"ref_image_path": map[string]interface{}{
							"type":        "string",
							"description": "Optional: Absolute path to a local reference image for Image-to-Image",
						},
					},
					"required": []string{"prompt"},
				},
			},
			{
				"name":        "generate_video",
				"description": "Generate a short cinematic video (2-10 seconds) using Gemini Video. Supports optional starting image (I2V).",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{
							"type":        "string",
							"description": "Detailed text description of the video to generate.",
						},
						"start_image_path": map[string]interface{}{
							"type":        "string",
							"description": "Optional: Absolute path to a local image for the first frame",
						},
					},
					"required": []string{"prompt"},
				},
			},
			{
				"name":        "generate_music",
				"description": "Generate a music beat or song using Gemini Music from a text prompt.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{
							"type":        "string",
							"description": "Detailed description of the music style, mood, instruments, and tempo.",
						},
					},
					"required": []string{"prompt"},
				},
			},
			{
				"name":        "chat",
				"description": "Chat with Google Gemini 3.8. Supports optional multimodal analysis by attaching local image/video files.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"prompt": map[string]interface{}{
							"type":        "string",
							"description": "The message or question to send to Gemini",
						},
						"ref_image_path": map[string]interface{}{
							"type":        "string",
							"description": "Optional: Absolute path to local image to analyze",
						},
						"ref_video_path": map[string]interface{}{
							"type":        "string",
							"description": "Optional: Absolute path to local video (.mp4) to analyze",
						},
					},
					"required": []string{"prompt"},
				},
			},
			{
				"name":        "reset_session",
				"description": "Reset active chat and generation session to start fresh.",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
					"required":   []string{},
				},
			},
			{
				"name":        "check_health",
				"description": "Check if the Gemini API backend is healthy and responsive.",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
					"required":   []string{},
				},
			},
		},
	}
}

func callMCPTool(toolName string, args PromptArg, backendURL string) (*CallToolResult, error) {
	log.Printf("🔧 Executing MCP tool: %s | prompt: %.80s...", toolName, args.Prompt)

	switch toolName {
	case "generate_image", "generate_video":
		return handleMCPMediaGeneration(toolName, args, backendURL)
	case "generate_music":
		return handleMCPMusicGeneration(args, backendURL)
	case "chat":
		return handleMCPChat(args, backendURL)
	case "reset_session":
		return handleMCPResetSession(backendURL)
	case "check_health":
		return handleMCPHealthCheck(backendURL)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func handleMCPMediaGeneration(toolName string, args PromptArg, backendURL string) (*CallToolResult, error) {
	var imagePath string
	if toolName == "generate_image" {
		imagePath = args.RefImagePath
	} else {
		imagePath = args.StartImagePath
	}

	if imagePath != "" {
		if _, err := os.Stat(imagePath); os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", imagePath)
		}
	}

	var doRequest func() (*http.Response, error)

	if imagePath != "" {
		log.Printf("📎 Using reference file: %s", imagePath)
		doRequest = func() (*http.Response, error) {
			return postMCPMultipart(backendURL+"/chat", args.Prompt, "mcp_client_media", true, imagePath)
		}
	} else {
		doRequest = func() (*http.Response, error) {
			payload := map[string]interface{}{
				"prompt":   args.Prompt,
				"user_id":  "mcp_client_media",
				"new_chat": true,
			}
			data, _ := json.Marshal(payload)
			client := &http.Client{Timeout: mcpHttpTimeout}
			return client.Post(backendURL+"/chat", "application/json", bytes.NewBuffer(data))
		}
	}

	body, err := executeMCPWithRetry(doRequest)
	if err != nil {
		return nil, err
	}

	var mresp struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		Videos []string `json:"videos"`
	}
	if err := json.Unmarshal(body, &mresp); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	var msg string
	if toolName == "generate_image" && len(mresp.Images) > 0 {
		msg = fmt.Sprintf("🎨 Image generated successfully!\nURL: %s", mresp.Images[0])
		if len(mresp.Images) > 1 {
			msg += "\n\nAdditional images:"
			for i, img := range mresp.Images[1:] {
				msg += fmt.Sprintf("\n  %d. %s", i+2, img)
			}
		}
	} else if toolName == "generate_video" && len(mresp.Videos) > 0 {
		msg = fmt.Sprintf("🎬 Video generated successfully!\nURL: %s", mresp.Videos[0])
	} else {
		msg = fmt.Sprintf("⚠️ Request processed but no media URL returned.\nGemini replied: %s", mresp.Text)
	}

	return &CallToolResult{
		Content: []TextContent{{Type: "text", Text: msg}},
	}, nil
}

func handleMCPMusicGeneration(args PromptArg, backendURL string) (*CallToolResult, error) {
	doRequest := func() (*http.Response, error) {
		payload := map[string]interface{}{
			"prompt":   args.Prompt,
			"user_id":  "mcp_client_music",
			"new_chat": true,
		}
		data, _ := json.Marshal(payload)
		client := &http.Client{Timeout: mcpHttpTimeout}
		return client.Post(backendURL+"/music", "application/json", bytes.NewBuffer(data))
	}

	body, err := executeMCPWithRetry(doRequest)
	if err != nil {
		return nil, err
	}

	var mresp struct {
		Text  string `json:"text"`
		Music []struct {
			LocalPath   string `json:"local_path"`
			DownloadURL string `json:"download_url"`
			Title       string `json:"title"`
		} `json:"music"`
	}
	if err := json.Unmarshal(body, &mresp); err != nil {
		return nil, fmt.Errorf("failed to parse music response: %w", err)
	}

	var msg string
	if len(mresp.Music) > 0 {
		track := mresp.Music[0]
		msg = fmt.Sprintf("🎵 Music generated successfully!\nTitle: %s\nPath: %s", track.Title, track.LocalPath)
		if track.DownloadURL != "" {
			msg += fmt.Sprintf("\nURL: %s", track.DownloadURL)
		}
	} else {
		msg = fmt.Sprintf("⚠️ Music request processed but no track returned.\nGemini replied: %s", mresp.Text)
	}

	return &CallToolResult{
		Content: []TextContent{{Type: "text", Text: msg}},
	}, nil
}

func handleMCPChat(args PromptArg, backendURL string) (*CallToolResult, error) {
	mediaPath := ""
	if args.RefImagePath != "" {
		mediaPath = args.RefImagePath
	} else if args.RefVideoPath != "" {
		mediaPath = args.RefVideoPath
	}

	if mediaPath != "" {
		if _, err := os.Stat(mediaPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", mediaPath)
		}
	}

	var doRequest func() (*http.Response, error)

	if mediaPath != "" {
		log.Printf("📎 Uploading media for chat: %s", mediaPath)
		doRequest = func() (*http.Response, error) {
			return postMCPMultipart(backendURL+"/chat", args.Prompt, "mcp_client_chat", false, mediaPath)
		}
	} else {
		doRequest = func() (*http.Response, error) {
			payload := map[string]interface{}{
				"prompt":   args.Prompt,
				"user_id":  "mcp_client_chat",
				"new_chat": false,
			}
			data, _ := json.Marshal(payload)
			client := &http.Client{Timeout: mcpHttpTimeout}
			return client.Post(backendURL+"/chat", "application/json", bytes.NewBuffer(data))
		}
	}

	body, err := executeMCPWithRetry(doRequest)
	if err != nil {
		return nil, err
	}

	var mresp struct {
		Text   string   `json:"text"`
		Images []string `json:"images"`
		Videos []string `json:"videos"`
	}
	if err := json.Unmarshal(body, &mresp); err != nil {
		return nil, fmt.Errorf("failed to parse chat response: %w", err)
	}

	msg := mresp.Text
	if len(mresp.Images) > 0 {
		msg += "\n\n📸 Generated Images:"
		for _, img := range mresp.Images {
			msg += fmt.Sprintf("\n  %s", img)
		}
	}
	if len(mresp.Videos) > 0 {
		msg += "\n\n🎬 Generated Videos:"
		for _, vid := range mresp.Videos {
			msg += fmt.Sprintf("\n  %s", vid)
		}
	}

	return &CallToolResult{
		Content: []TextContent{{Type: "text", Text: msg}},
	}, nil
}

func handleMCPResetSession(backendURL string) (*CallToolResult, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	payload := map[string]interface{}{
		"user_id": "mcp_client_chat",
	}
	data, _ := json.Marshal(payload)

	resp, err := client.Post(backendURL+"/reset", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error: %s", string(body))
	}

	return &CallToolResult{
		Content: []TextContent{
			{Type: "text", Text: "🔄 Session successfully reset! The next messages will start a fresh conversation."},
		},
	}, nil
}

func handleMCPHealthCheck(backendURL string) (*CallToolResult, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Get(backendURL + "/health")
	elapsed := time.Since(start)

	if err != nil {
		return &CallToolResult{
			Content: []TextContent{{Type: "text", Text: fmt.Sprintf("❌ Backend API unreachable: %v", err)}},
			IsError: true,
		}, nil
	}
	defer resp.Body.Close()

	msg := fmt.Sprintf("✅ Backend API is healthy\n   URL: %s\n   Status: %d\n   Latency: %dms",
		backendURL, resp.StatusCode, elapsed.Milliseconds())

	return &CallToolResult{
		Content: []TextContent{{Type: "text", Text: msg}},
	}, nil
}

func executeMCPWithRetry(doRequest func() (*http.Response, error)) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt < mcpMaxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(math.Pow(2, float64(attempt))) * mcpBaseRetryWait
			log.Printf("🔄 Retry %d/%d after %v...", attempt+1, mcpMaxRetries, wait)
			time.Sleep(wait)
		}

		resp, err := doRequest()
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 200 {
			return body, nil
		}

		lastErr = fmt.Errorf("[%d] %s", resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("all %d attempts failed. Last error: %v", mcpMaxRetries, lastErr)
}

func formatMCPToolError(err error) string {
	return fmt.Sprintf("❌ Tool execution failed: %v", err)
}

func postMCPMultipart(targetURL, prompt, userID string, newChat bool, filePath string) (*http.Response, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	w.WriteField("prompt", prompt)
	w.WriteField("user_id", userID)
	if newChat {
		w.WriteField("new_chat", "true")
	}

	if filePath != "" {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open file '%s': %w", filePath, err)
		}
		defer file.Close()

		part, err := w.CreateFormFile("image", filepath.Base(filePath))
		if err != nil {
			return nil, fmt.Errorf("failed to create form file: %w", err)
		}
		io.Copy(part, file)
	}

	w.Close()

	req, err := http.NewRequest("POST", targetURL, &b)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: mcpHttpTimeout}
	return client.Do(req)
}

func sendMCPResult(id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	sendMCP(resp)
}

func sendMCPError(id interface{}, code int, message string, data interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: map[string]interface{}{
			"code":    code,
			"message": message,
			"data":    data,
		},
	}
	sendMCP(resp)
}

func sendMCP(val interface{}) {
	data, err := json.Marshal(val)
	if err != nil {
		log.Printf("❌ Failed to marshal JSON-RPC message: %v", err)
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}
