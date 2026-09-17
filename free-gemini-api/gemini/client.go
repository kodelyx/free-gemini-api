package gemini

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	stdhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

var chromeHeaderOrder = []string{
	"host",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"sec-ch-ua-arch",
	"sec-ch-ua-bitness",
	"sec-ch-ua-full-version",
	"user-agent",
	"accept",
	"accept-encoding",
	"accept-language",
	"cookie",
	"content-type",
	"origin",
	"referer",
	"x-same-domain",
	"x-goog-ext-525001261-jspb",
	"x-goog-ext-525005358-jspb",
	"x-goog-ext-73010989-jspb",
	"priority",
}

var chromePseudoHeaderOrder = []string{
	":method",
	":authority",
	":scheme",
	":path",
}

func getPlatformDetails() (ua string, platform string, secChUa string) {
	switch runtime.GOOS {
	case "windows":
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36", `"Windows"`, `"Not(A:Brand";v="99", "Google Chrome";v="152", "Chromium";v="152"`
	case "linux":
		return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36", `"Linux"`, `"Not(A:Brand";v="99", "Google Chrome";v="152", "Chromium";v="152"`
	default: // macOS
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36", `"macOS"`, `"Not(A:Brand";v="99", "Google Chrome";v="152", "Chromium";v="152"`
	}
}

var (
	CurrentChromeUA, CurrentPlatform, CurrentSecChUa = getPlatformDetails()
)

// Flash model header value (hardcoded since we only use Flash)
const flashModelID = "56fdd199312815e2"

var flashHeaderValue = fmt.Sprintf(`[1,null,null,null,"%s",null,null,0,[4],null,null,2]`, flashModelID)

type SavedSessionData struct {
	SNlM0e         string `json:"snlm0e"`
	FSID           string `json:"fsid"`
	BL             string `json:"bl"`
	SavedAt        int64  `json:"saved_at"`
	RawCookiesHash string `json:"raw_cookies_hash"`
}

type GeminiClient struct {
	client         tls_client.HttpClient
	quicClient     *stdhttp.Client
	useQUIC        bool
	cookiesFile    string
	SNlM0e         string
	FSID           string
	BL             string
	ReqID          int
	ConversationID string
	ResponseID     string
	ChoiceID       string
	IsInitialized  bool
	RawCookies     string
	SessionSavedAt int64
}

type CookieObject struct {
	Domain         string  `json:"domain"`
	ExpirationDate float64 `json:"expirationDate,omitempty"`
	HostOnly       bool    `json:"hostOnly,omitempty"`
	HttpOnly       bool    `json:"httpOnly,omitempty"`
	Name           string  `json:"name"`
	Path           string  `json:"path"`
	SameSite       string  `json:"sameSite,omitempty"`
	Secure         bool    `json:"secure,omitempty"`
	Session        bool    `json:"session,omitempty"`
	StoreId        string  `json:"storeId,omitempty"`
	Value          string  `json:"value"`
}

func NewClient(cookiesFile string) (*GeminiClient, error) {
	jar := tls_client.NewCookieJar()
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(300),
		tls_client.WithClientProfile(profiles.Chrome_152),
		tls_client.WithCookieJar(jar),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}

	// Initialize HTTP/3 QUIC transport with 0-RTT session resumption
	quicTransport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
			MinVersion:         tls.VersionTLS13,
			ClientSessionCache: tls.NewLRUClientSessionCache(32),
		},
		QUICConfig: &quic.Config{
			MaxIdleTimeout:  30 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
		},
	}
	quicClient := &stdhttp.Client{
		Timeout:   10 * time.Second,
		Transport: quicTransport,
	}

	c := &GeminiClient{
		client:      client,
		quicClient: quicClient,
		useQUIC:     true,
		cookiesFile: cookiesFile,
		ReqID:       rand.Intn(9000000) + 1000000,
	}

	if err := c.loadCookies(); err != nil {
		return nil, fmt.Errorf("failed to load cookies from %s: %w", cookiesFile, err)
	}

	return c, nil
}

func (c *GeminiClient) loadCookies() error {
	data, err := os.ReadFile(c.cookiesFile)
	if err != nil {
		return err
	}

	// JSON Array Format
	var cookieList []CookieObject
	if err := json.Unmarshal(data, &cookieList); err == nil && len(cookieList) > 0 {
		log.Printf("Loading cookies in JSON array format (%d cookies)", len(cookieList))
		var rawParts []string
		for _, ck := range cookieList {
			domain := strings.TrimPrefix(ck.Domain, ".")
			u, _ := url.Parse("https://" + domain)
			c.client.GetCookieJar().SetCookies(u, []*http.Cookie{
				{
					Name:   ck.Name,
					Value:  ck.Value,
					Domain: ck.Domain,
					Path:   ck.Path,
					Secure: ck.Secure,
				},
			})
			rawParts = append(rawParts, fmt.Sprintf("%s=%s", ck.Name, ck.Value))
		}
		c.RawCookies = strings.Join(rawParts, "; ")
		return nil
	}

	// Fallback: Legacy Object Format
	var cookieData struct {
		Cookies   string  `json:"cookies"`
		UpdatedAt float64 `json:"updated_at"`
	}

	if err := json.Unmarshal(data, &cookieData); err == nil && cookieData.Cookies != "" {
		log.Printf("Loading cookies in legacy string format")
		c.RawCookies = cookieData.Cookies
		u, _ := url.Parse("https://gemini.google.com")
		cookies := parseCookieString(cookieData.Cookies, ".google.com")
		c.client.GetCookieJar().SetCookies(u, cookies)
		return nil
	}

	return fmt.Errorf("unsupported cookie format in %s", c.cookiesFile)
}

func parseCookieString(raw, domain string) []*http.Cookie {
	var cookies []*http.Cookie
	parts := strings.Split(raw, "; ")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			cookies = append(cookies, &http.Cookie{
				Name:   strings.TrimSpace(kv[0]),
				Value:  strings.TrimSpace(kv[1]),
				Domain: domain,
				Path:   "/",
			})
		}
	}
	return cookies
}

func (c *GeminiClient) getSessionCachePath() string {
	base := filepath.Base(c.cookiesFile)
	cleanName := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join("cookies", fmt.Sprintf("session_%s.json", cleanName))
}

func (c *GeminiClient) hashCookies() string {
	h := sha256.Sum256([]byte(c.RawCookies))
	return hex.EncodeToString(h[:])
}

func (c *GeminiClient) loadCachedSession() bool {
	cachePath := c.getSessionCachePath()
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}
	var cached SavedSessionData
	if err := json.Unmarshal(data, &cached); err != nil {
		return false
	}

	// Valid for 18 hours and requires matching cookies hash
	if cached.SNlM0e != "" && cached.RawCookiesHash == c.hashCookies() &&
		time.Since(time.Unix(cached.SavedAt, 0)) < 18*time.Hour {
		c.SNlM0e = cached.SNlM0e
		c.FSID = cached.FSID
		c.BL = cached.BL
		c.SessionSavedAt = cached.SavedAt
		c.IsInitialized = true
		log.Printf("⚡ [Zero-Wait Instant Start] Loaded cached session for %s (0s bootstrap!)", cachePath)
		return true
	}
	return false
}

func (c *GeminiClient) persistSession() {
	if c.SNlM0e == "" {
		return
	}
	cachePath := c.getSessionCachePath()
	saved := SavedSessionData{
		SNlM0e:         c.SNlM0e,
		FSID:           c.FSID,
		BL:             c.BL,
		SavedAt:        time.Now().Unix(),
		RawCookiesHash: c.hashCookies(),
	}
	if data, err := json.MarshalIndent(saved, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
		_ = os.WriteFile(cachePath, data, 0644)
		log.Printf("💾 Persisted fresh session tokens to %s (valid for 18h)", cachePath)
	}
}

func (c *GeminiClient) InitSession() error {
	// Step 1: Check instant session cache (0 roundtrips)
	if c.loadCachedSession() {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		req, err := http.NewRequest("GET", "https://gemini.google.com/app", nil)
		if err != nil {
			return err
		}

		// Apply exact Chrome JA4/JA4H HTTP/2 Headers & Pseudo-Headers
		req.Header[http.HeaderOrderKey] = chromeHeaderOrder
		req.Header[http.PHeaderOrderKey] = chromePseudoHeaderOrder

		req.Header.Set("User-Agent", CurrentChromeUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		req.Header.Set("sec-ch-ua", CurrentSecChUa)
		req.Header.Set("sec-ch-ua-mobile", "?0")
		req.Header.Set("sec-ch-ua-platform", CurrentPlatform)
		req.Header.Set("Referer", "https://gemini.google.com/")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(1 * time.Second)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = fmt.Errorf("gemini app failed: status %d (check cookies)", resp.StatusCode)
			time.Sleep(1 * time.Second)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		bodyStr := string(body)

		snlm0eRe := regexp.MustCompile(`"SNlM0e":"(.*?)"`)
		fsidRe := regexp.MustCompile(`"FdrFJe":"(.*?)"`)
		cfb2hRe := regexp.MustCompile(`"cfb2h":"(.*?)"`)

		if m := snlm0eRe.FindStringSubmatch(bodyStr); len(m) > 1 {
			c.SNlM0e = m[1]
			log.Printf("Session Initialized (attempt %d). Token size: %d", attempt, len(c.SNlM0e))
			if fm := fsidRe.FindStringSubmatch(bodyStr); len(fm) > 1 {
				c.FSID = fm[1]
			}
			if cm := cfb2hRe.FindStringSubmatch(bodyStr); len(cm) > 1 {
				c.BL = cm[1]
			}
			c.IsInitialized = true
			c.persistSession()
			return nil
		}

		if strings.Contains(bodyStr, "ServiceLogin") || strings.Contains(bodyStr, "login.google.com") {
			if attempt == 1 {
				log.Println("⚠️ Session expired detected. Requesting Chrome Extension to proactively rotate cookies...")
				BroadcastCookieRefresh()
				time.Sleep(5 * time.Second)
				_ = c.loadCookies()
				continue
			}
			return fmt.Errorf("session expired: Google redirected to login. Please refresh cookies")
		}
		lastErr = fmt.Errorf("SNlM0e not found - Google might have changed UI or blocked the request")
	}

	return lastErr
}

func (c *GeminiClient) ensureInit() error {
	if !c.IsInitialized {
		return c.InitSession()
	}
	return nil
}

// executeWithRetry executes a request function. If it fails, it refreshes session and retries.
func (c *GeminiClient) executeWithRetry(actionName string, runFunc func() error) error {
	err := runFunc()
	if err == nil {
		return nil
	}

	log.Printf("⚠️ [%s] Request failed: %v. Triggering automatic cookie sync and session refresh...", actionName, err)
	
	// Reset initialized status to force session re-initialization
	c.IsInitialized = false
	
	// InitSession will broadcast cookie refresh, sleep 5s, reload cookies, and retry the app session init
	if initErr := c.InitSession(); initErr != nil {
		log.Printf("❌ [%s] Session recovery failed: %v", actionName, initErr)
		return fmt.Errorf("%s failed and auto-heal session recovery failed: %w", actionName, initErr)
	}

	log.Printf("🔄 [%s] Session successfully auto-healed! Retrying request...", actionName)
	return runFunc()
}

func (c *GeminiClient) Ask(prompt string) (*GeminiResponse, error) {
	return c.AskWithTool(prompt, "")
}

// AskStream sends a prompt and streams text chunks via callback as they arrive
func (c *GeminiClient) AskStream(prompt string, onChunk func(text string)) (*GeminiResponse, error) {
	start := time.Now()
	
	var response *GeminiResponse
	execErr := c.executeWithRetry("AskStream", func() error {
		var err error
		response, err = c.executeStreamRequest(prompt, onChunk)
		return err
	})

	if execErr != nil {
		return nil, execErr
	}

	response.Elapsed = time.Since(start).Seconds()
	return response, nil
}

// executeStreamRequest performs the actual streaming call with HTTP/3 QUIC + HTTP/2 fallback
func (c *GeminiClient) executeStreamRequest(prompt string, onChunk func(text string)) (*GeminiResponse, error) {
	if err := c.ensureInit(); err != nil {
		return nil, err
	}

	// Build request (same payload as sendRequest)
	c.ReqID += 100
	reqID := fmt.Sprintf("%d", c.ReqID)

	msgInner := []interface{}{prompt, 0, nil, nil, nil, nil, 0}
	msgLang := []string{"en-GB"}
	msgContext := []interface{}{c.ConversationID, c.ResponseID, c.ChoiceID, nil, nil, nil, nil, nil, nil, ""}
	msgStruct := []interface{}{msgInner, msgLang, msgContext}

	msgJSON, _ := json.Marshal(msgStruct)
	fReqVal := []interface{}{nil, string(msgJSON)}
	fReqJSON, _ := json.Marshal(fReqVal)

	data := url.Values{}
	data.Set("f.req", string(fReqJSON))
	data.Set("at", c.SNlM0e)

	urlStr := "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	query := url.Values{}
	query.Add("bl", c.BL)
	query.Add("_reqid", reqID)
	query.Add("rt", "c")
	if c.FSID != "" {
		query.Add("f.sid", c.FSID)
	}
	fullURL := urlStr + "?" + query.Encode()

	var rawBody string
	var err error

	if c.useQUIC {
		rawBody, err = c.doStreamRequestQUIC(fullURL, data, onChunk)
		if err != nil {
			log.Printf("⚠️ HTTP/3 QUIC stream failed (%v). Falling back to HTTP/2 TLS...", err)
			c.useQUIC = false
			rawBody, err = c.doStreamRequestHTTP2(fullURL, data, onChunk)
		}
	} else {
		rawBody, err = c.doStreamRequestHTTP2(fullURL, data, onChunk)
	}

	if err != nil {
		return nil, err
	}

	response := &GeminiResponse{}
	parseResponse(rawBody, response)

	if response.ConversationID != "" {
		c.ConversationID = response.ConversationID
		c.ResponseID = response.ResponseID
		c.ChoiceID = response.ChoiceID
	}

	// Video auto-detect
	if response.ConversationID != "" &&
		(strings.Contains(response.Text, "video_gen_chip") || strings.Contains(response.Text, "generating your video")) {
		c.pollVideoURL(response)
	}

	if response.ConversationID == "" && response.Text == "" {
		return nil, fmt.Errorf("empty stream response received")
	}

	return response, nil
}

func (c *GeminiClient) doStreamRequestQUIC(fullURL string, data url.Values, onChunk func(text string)) (string, error) {
	req, err := stdhttp.NewRequest("POST", fullURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("x-goog-ext-525001261-jspb", flashHeaderValue)
	req.Header.Set("x-goog-ext-525005358-jspb", `["DIRECT-API-SESSION",1]`)
	req.Header.Set("x-goog-ext-73010989-jspb", `[0]`)
	if c.RawCookies != "" {
		req.Header.Set("Cookie", c.RawCookies)
	}

	resp, err := c.quicClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("QUIC status %d", resp.StatusCode)
	}

	return c.readAndProcessStream(resp.Body, onChunk)
}

func (c *GeminiClient) doStreamRequestHTTP2(fullURL string, data url.Values, onChunk func(text string)) (string, error) {
	req, err := http.NewRequest("POST", fullURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header[http.HeaderOrderKey] = chromeHeaderOrder
	req.Header[http.PHeaderOrderKey] = chromePseudoHeaderOrder

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("x-goog-ext-525001261-jspb", flashHeaderValue)
	req.Header.Set("x-goog-ext-525005358-jspb", `["DIRECT-API-SESSION",1]`)
	req.Header.Set("x-goog-ext-73010989-jspb", `[0]`)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP2 status %d", resp.StatusCode)
	}

	return c.readAndProcessStream(resp.Body, onChunk)
}

func (c *GeminiClient) readAndProcessStream(body io.Reader, onChunk func(text string)) (string, error) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	rawBody := string(bodyBytes)

	prevText := ""
	lines := strings.Split(rawBody, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "[[\"wrb.fr\"") {
			continue
		}

		tempResp := &GeminiResponse{}
		parseResponse(line, tempResp)

		if tempResp.Text != "" && tempResp.Text != prevText {
			delta := tempResp.Text
			if strings.HasPrefix(delta, prevText) {
				delta = delta[len(prevText):]
			}
			if delta != "" && onChunk != nil {
				onChunk(delta)
			}
			prevText = tempResp.Text
		}
	}

	return rawBody, nil
}

func (c *GeminiClient) UploadImage(imageBytes []byte, filename string, mimeType string) (string, error) {
	if err := c.ensureInit(); err != nil {
		return "", err
	}

	urlStr := "https://push.clients6.google.com/upload/"
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(""))
	if err != nil {
		return "", err
	}

	req.Header.Set("x-goog-upload-protocol", "resumable")
	req.Header.Set("x-goog-upload-command", "start")
	req.Header.Set("x-tenant-id", "bard-storage")
	req.Header.Set("x-goog-upload-header-content-length", fmt.Sprintf("%d", len(imageBytes)))
	req.Header.Set("x-goog-upload-header-content-type", mimeType)
	req.Header.Set("push-id", "feeds/mcudyrk2a4khkz")
	req.Header.Set("sec-ch-ua", CurrentSecChUa)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", CurrentPlatform)
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("UploadImage start failed: Status %d", resp.StatusCode)
	}

	uploadURL := resp.Header.Get("X-Goog-Upload-Url")
	if uploadURL == "" {
		uploadURL = resp.Header.Get("x-goog-upload-url")
	}
	if uploadURL == "" {
		return "", fmt.Errorf("UploadImage start failed: missing x-goog-upload-url header")
	}

	req2, err := http.NewRequest("POST", uploadURL, strings.NewReader(string(imageBytes)))
	if err != nil {
		return "", err
	}

	req2.Header.Set("x-goog-upload-protocol", "resumable")
	req2.Header.Set("x-goog-upload-command", "upload, finalize")
	req2.Header.Set("x-goog-upload-offset", "0")
	req2.Header.Set("x-tenant-id", "bard-storage")
	req2.Header.Set("Content-Type", mimeType)
	req2.Header.Set("sec-ch-ua", CurrentSecChUa)
	req2.Header.Set("sec-ch-ua-mobile", "?0")
	req2.Header.Set("sec-ch-ua-platform", CurrentPlatform)
	req2.Header.Set("Origin", "https://gemini.google.com")
	req2.Header.Set("Referer", "https://gemini.google.com/")
	req2.Header.Set("User-Agent", CurrentChromeUA)

	resp2, err := c.client.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		return "", fmt.Errorf("UploadImage finalize failed: Status %d", resp2.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp2.Body)
	return string(bodyBytes), nil
}

// ImageInput holds data for a single image to upload
type ImageInput struct {
	Data     []byte
	Filename string
	MimeType string
}

func (c *GeminiClient) AskWithImage(prompt string, imageBytes []byte, filename string, mimeType string) (*GeminiResponse, error) {
	return c.AskWithImages(prompt, []ImageInput{{Data: imageBytes, Filename: filename, MimeType: mimeType}})
}

func (c *GeminiClient) AskWithImages(prompt string, images []ImageInput) (*GeminiResponse, error) {
	start := time.Now()

	if err := c.ensureInit(); err != nil {
		return nil, err
	}

	// Upload all images and build imageRef array
	var imageRef []interface{}
	uploadErr := c.executeWithRetry("UploadImages", func() error {
		imageRef = nil // Reset list for retry
		for i, img := range images {
			mediaRefPath, err := c.UploadImage(img.Data, img.Filename, img.MimeType)
			if err != nil {
				return err
			}
			mediaRefPath = strings.TrimSpace(mediaRefPath)
			log.Printf("📸 Image %d/%d uploaded: ref=[%s]", i+1, len(images), mediaRefPath)

			imageRef = append(imageRef, []interface{}{
				[]interface{}{mediaRefPath, 1, nil, img.MimeType},
				img.Filename,
				nil, nil, nil, nil, nil, nil,
				[]int{0},
			})
		}
		return nil
	})

	if uploadErr != nil {
		return nil, uploadErr
	}

	var response *GeminiResponse
	var err error
	execErr := c.executeWithRetry("AskWithImages", func() error {
		_, response, err = c.sendRequest(prompt, "", imageRef)
		if err != nil {
			return err
		}
		if response.ConversationID == "" && response.Text == "" {
			return fmt.Errorf("empty response received with images (possible session expiry)")
		}
		return nil
	})

	if execErr != nil {
		return nil, execErr
	}

	if response.ConversationID != "" {
		c.ConversationID = response.ConversationID
		c.ResponseID = response.ResponseID
		c.ChoiceID = response.ChoiceID
	}

	// Auto-detect video generation and poll for video URL
	if response.ConversationID != "" &&
		(strings.Contains(response.Text, "video_gen_chip") || strings.Contains(response.Text, "generating your video") || strings.Contains(response.Text, "video is being generated")) {
		c.pollVideoURL(response)
	}

	response.Elapsed = time.Since(start).Seconds()
	return response, nil
}

// pollVideoURL polls hNvQHb RPC to find the video download URL
func (c *GeminiClient) pollVideoURL(response *GeminiResponse) {
	log.Println("🎬 Polling hNvQHb for video download URL...")
	vidRe := regexp.MustCompile(`https?://contribution\.usercontent\.google\.com/download\??[^"\\` + "`" + `\s]+`)

	delays := []time.Duration{2500 * time.Millisecond, 3000 * time.Millisecond, 4000 * time.Millisecond}
	for i := 0; i < 60; i++ {
		sleepDur := 5 * time.Second
		if i < len(delays) {
			sleepDur = delays[i]
		}
		time.Sleep(sleepDur)
		log.Printf("🎬 Video Poll %d/60 (waited %v)...", i+1, sleepDur)

		hPayload := fmt.Sprintf(`["%s",10,null,1,[1],[4],null,1]`, response.ConversationID)
		hBody, err := c.CallRPC("hNvQHb", hPayload)
		if err != nil {
			log.Printf("⚠️ hNvQHb error: %v", err)
			continue
		}

		hBody = strings.ReplaceAll(hBody, `\\u0026`, "&")
		hBody = strings.ReplaceAll(hBody, `\\u003d`, "=")
		hBody = strings.ReplaceAll(hBody, `\u0026`, "&")
		hBody = strings.ReplaceAll(hBody, `\u003d`, "=")

		if strings.Contains(hBody, "too many requests") || strings.Contains(hBody, "a lot of requests") ||
			strings.Contains(hBody, "try again later") || strings.Contains(hBody, "couldn't do that") ||
			strings.Contains(hBody, "can't create more videos") || strings.Contains(hBody, "can\\'t create more videos") ||
			strings.Contains(hBody, "I can't create") || strings.Contains(hBody, "find videos from the web") ||
			strings.Contains(hBody, "और ज़्यादा वीडियो") || strings.Contains(hBody, "नहीं जनरेट कर सकता") ||
			strings.Contains(hBody, "ढूँढ सकता हूँ") || strings.Contains(hBody, "limit") {
			log.Println("❌ Video generation refused (rate limit/quota exhausted)")
			response.Text = "Video generation failed: daily limit/quota exhausted."
			response.Videos = nil
			break
		}

		// Find ALL matched URLs
		matches := vidRe.FindAllString(hBody, -1)
		var chosenURL string
		for _, m := range matches {
			// Skip reference input videos uploaded by our server (which always contain "vid_")
			if strings.Contains(m, "vid_") {
				log.Printf("⏭️ Skipping matched reference video URL: %s", m[:min(len(m), 100)])
				continue
			}
			// Skip non-video assets like protobuf context files (ensure it ends with .mp4 or contains filename=video.mp4)
			if !strings.Contains(m, ".mp4") && !strings.Contains(m, "filename=video") {
				log.Printf("⏭️ Skipping matched non-mp4 asset URL: %s", m[:min(len(m), 100)])
				continue
			}
			chosenURL = m
			break
		}

		if chosenURL != "" {
			response.Videos = []string{chosenURL}
			log.Println("✅ Video URL found!")
			break
		}

		if strings.Contains(hBody, "Your video is ready") {
			log.Println("📝 Video is ready but URL not found in expected format, continuing...")
		}

		// Debug: log tail of response
		snippet := hBody
		if len(snippet) > 200 {
			snippet = hBody[len(hBody)-200:]
		}
		log.Printf("🔍 Poll response (%d chars): ...%s", len(hBody), snippet)
	}
}

func (c *GeminiClient) AskWithTool(prompt string, tool string) (*GeminiResponse, error) {
	start := time.Now()
	if err := c.ensureInit(); err != nil {
		return nil, err
	}

	var response *GeminiResponse
	var err error
	execErr := c.executeWithRetry("AskWithTool", func() error {
		_, response, err = c.sendRequest(prompt, tool, nil)
		if err != nil {
			return err
		}
		if response.ConversationID == "" && response.Text == "" {
			return fmt.Errorf("empty response received (possible session expiry)")
		}
		return nil
	})

	if execErr != nil {
		return nil, execErr
	}

	if response.ConversationID != "" {
		c.ConversationID = response.ConversationID
		c.ResponseID = response.ResponseID
		c.ChoiceID = response.ChoiceID
	}

	// Music fallback: if URL didn't come in first response, quick poll
	if tool == "music_gen" && len(response.Music) == 0 && response.ConversationID != "" {
		log.Println("🎵 Music URL not in response, polling...")
		rawConv := strings.TrimPrefix(response.ConversationID, "c_")
		re := regexp.MustCompile(`https?://contribution\.usercontent\.google\.com/download[^"\\` + "`" + `\s]+`)

		for i := 0; i < 6; i++ {
			time.Sleep(5 * time.Second)
			body, err := c.pollConversation(rawConv)
			if err != nil {
				continue
			}
			body = strings.ReplaceAll(body, `\u0026`, "&")
			body = strings.ReplaceAll(body, `\u003d`, "=")
			if m := re.FindString(body); m != "" {
				track := MusicTrack{DownloadURL: m}
				if idx := strings.Index(m, "filename="); idx != -1 {
					fname := m[idx+9:]
					if ai := strings.Index(fname, "&"); ai != -1 {
						fname = fname[:ai]
					}
					fname = strings.ReplaceAll(fname, "_", " ")
					fname = strings.TrimSuffix(strings.TrimSuffix(fname, ".mp3"), ".mp4")
					track.Title = fname
				}
				response.Music = append(response.Music, track)
				log.Println("✅ Music URL found via poll")
				break
			}
		}
	}

	// Auto-detect video generation and poll
	if tool == "" && response.ConversationID != "" &&
		(strings.Contains(response.Text, "video_gen_chip") || strings.Contains(response.Text, "generating your video")) {
		c.pollVideoURL(response)
	}

	response.Elapsed = time.Since(start).Seconds()
	return response, nil
}

// AskVideo generates a video via chat prompt and polls hNvQHb for download URL.
// Flow: sendRequest(prompt) → poll hNvQHb(convID) for contribution.usercontent URL
func (c *GeminiClient) AskVideo(prompt string) (*GeminiResponse, error) {
	start := time.Now()
	if err := c.ensureInit(); err != nil {
		return nil, err
	}

	// Ensure prompt triggers video generation
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "generate a video") && !strings.Contains(lower, "create a video") && !strings.Contains(lower, "make a video") {
		prompt = "Generate a video of: " + prompt
	}

	var response *GeminiResponse
	var err error
	execErr := c.executeWithRetry("AskVideo", func() error {
		_, response, err = c.sendRequest(prompt, "", nil)
		if err != nil {
			return err
		}
		if response.ConversationID == "" && response.Text == "" {
			return fmt.Errorf("empty response received for video (possible session expiry)")
		}
		return nil
	})

	if execErr != nil {
		return nil, execErr
	}

	if response.ConversationID != "" {
		c.ConversationID = response.ConversationID
		c.ResponseID = response.ResponseID
		c.ChoiceID = response.ChoiceID
	}

	log.Printf("🎬 Video response: conv=%s, text=%s", response.ConversationID, response.Text[:min(len(response.Text), 100)])

	// If no video_gen_chip in response, video wasn't triggered
	if !strings.Contains(response.Text, "video_gen_chip") && !strings.Contains(response.Text, "generating your video") {
		log.Println("⚠️ Video generation not triggered — Gemini didn't recognize video intent")
		response.Elapsed = time.Since(start).Seconds()
		return response, nil
	}

	if response.ConversationID == "" {
		response.Elapsed = time.Since(start).Seconds()
		return response, nil
	}

	c.pollVideoURL(response)

	response.Elapsed = time.Since(start).Seconds()
	return response, nil
}

// CallRPC sends a batchexecute request for any given RPC ID
func (c *GeminiClient) CallRPC(rpcID, payload string) (string, error) {
	c.ReqID += 100
	reqID := fmt.Sprintf("%d", c.ReqID)

	wrapperJSON, _ := json.Marshal([][][]interface{}{{{rpcID, payload, nil, "generic"}}})

	body := url.Values{}
	body.Set("f.req", string(wrapperJSON))
	body.Set("at", c.SNlM0e)

	urlStr := "https://gemini.google.com/_/BardChatUi/data/batchexecute"

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}

	q := req.URL.Query()
	q.Add("bl", c.BL)
	q.Add("_reqid", reqID)
	q.Add("rt", "c")
	if c.FSID != "" {
		q.Add("f.sid", c.FSID)
	}
	q.Add("rpcids", rpcID)
	q.Add("source-path", "/app")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("sec-ch-ua", CurrentSecChUa)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", CurrentPlatform)
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	return string(bodyBytes), nil
}

// pollConversation calls hNvQHb RPC (music-style payload) for music polling
func (c *GeminiClient) pollConversation(convID string) (string, error) {
	payload := fmt.Sprintf(`[null,"%s"]`, convID)
	return c.CallRPC("hNvQHb", payload)
}

func (c *GeminiClient) sendRequest(prompt string, tool string, imageRef []interface{}) (string, *GeminiResponse, error) {
	c.ReqID += 100
	reqID := fmt.Sprintf("%d", c.ReqID)

	msgInner := []interface{}{prompt, 0, nil, imageRef, nil, nil, 0}
	msgLang := []string{"en-GB"}
	msgContext := []interface{}{c.ConversationID, c.ResponseID, c.ChoiceID, nil, nil, nil, nil, nil, nil, ""}
	msgStruct := []interface{}{msgInner, msgLang, msgContext}

	if tool != "" {
		for len(msgStruct) < 33 {
			msgStruct = append(msgStruct, nil)
		}
		msgStruct = append(msgStruct, []string{tool})
	}

	msgJSON, _ := json.Marshal(msgStruct)
	fReqVal := []interface{}{nil, string(msgJSON)}
	fReqJSON, _ := json.Marshal(fReqVal)

	data := url.Values{}
	data.Set("f.req", string(fReqJSON))
	data.Set("at", c.SNlM0e)

	urlStr := "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	query := url.Values{}
	query.Add("bl", c.BL)
	query.Add("_reqid", reqID)
	query.Add("rt", "c")
	if c.FSID != "" {
		query.Add("f.sid", c.FSID)
	}
	fullURL := urlStr + "?" + query.Encode()

	var rawBody string
	var err error

	if c.useQUIC {
		rawBody, err = c.sendRequestQUIC(fullURL, data)
		if err != nil {
			log.Printf("⚠️ HTTP/3 QUIC request failed (%v). Falling back to HTTP/2 TLS...", err)
			c.useQUIC = false
			rawBody, err = c.sendRequestHTTP2(fullURL, data)
		}
	} else {
		rawBody, err = c.sendRequestHTTP2(fullURL, data)
	}

	if err != nil {
		return "", nil, err
	}

	log.Printf("Raw Body Length: %d, Response: %s", len(rawBody), rawBody[:min(len(rawBody), 500)])

	response := &GeminiResponse{}
	parseResponse(rawBody, response)

	// Direct regex scan for music download URLs in raw body
	if strings.Contains(rawBody, "contribution.usercontent.google.com") {
		musicRe := regexp.MustCompile(`https://contribution\.usercontent\.google\.com/download[^"\\)\}\s]*`)
		matches := musicRe.FindAllString(rawBody, -1)
		for _, m := range matches {
			m = strings.ReplaceAll(m, `\u0026`, "&")
			m = strings.ReplaceAll(m, `\\u0026`, "&")
			found := false
			for _, existing := range response.Music {
				if existing.DownloadURL == m {
					found = true
					break
				}
			}
			if !found {
				track := MusicTrack{DownloadURL: m}
				if idx := strings.Index(m, "filename="); idx != -1 {
					fname := m[idx+9:]
					if ampIdx := strings.Index(fname, "&"); ampIdx != -1 {
						fname = fname[:ampIdx]
					}
					fname = strings.ReplaceAll(fname, "_", " ")
					fname = strings.TrimSuffix(fname, ".mp3")
					fname = strings.TrimSuffix(fname, ".mp4")
					track.Title = fname
				}
				response.Music = append(response.Music, track)
				log.Printf("🎵 Found music URL via regex: %s", m[:min(len(m), 100)])
			}
		}
	}

	return rawBody, response, nil
}

func (c *GeminiClient) sendRequestQUIC(fullURL string, data url.Values) (string, error) {
	req, err := stdhttp.NewRequest("POST", fullURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("x-goog-ext-525001261-jspb", flashHeaderValue)
	req.Header.Set("x-goog-ext-525005358-jspb", `["DIRECT-API-SESSION",1]`)
	req.Header.Set("x-goog-ext-73010989-jspb", `[0]`)
	if c.RawCookies != "" {
		req.Header.Set("Cookie", c.RawCookies)
	}

	resp, err := c.quicClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("QUIC HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bodyBytes), nil
}

func (c *GeminiClient) sendRequestHTTP2(fullURL string, data url.Values) (string, error) {
	req, err := http.NewRequest("POST", fullURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header[http.HeaderOrderKey] = chromeHeaderOrder
	req.Header[http.PHeaderOrderKey] = chromePseudoHeaderOrder

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("x-goog-ext-525001261-jspb", flashHeaderValue)
	req.Header.Set("x-goog-ext-525005358-jspb", `["DIRECT-API-SESSION",1]`)
	req.Header.Set("x-goog-ext-73010989-jspb", `[0]`)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bodyBytes), nil
}

func parseResponse(raw string, res *GeminiResponse) {
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "[[") {
			continue
		}

		var wrapper [][]interface{}
		if err := json.Unmarshal([]byte(line), &wrapper); err != nil {
			continue
		}

		for _, item := range wrapper {
			if len(item) < 3 {
				continue
			}
			wrbID, ok := item[0].(string)
			if !ok || wrbID != "wrb.fr" {
				continue
			}

			payloadStr, ok := item[2].(string)
			if !ok {
				continue
			}

			var inner []interface{}
			if err := json.Unmarshal([]byte(payloadStr), &inner); err != nil {
				continue
			}

			if len(inner) > 1 {
				if ctxArr, ok := inner[1].([]interface{}); ok && len(ctxArr) >= 2 {
					if cid, ok := ctxArr[0].(string); ok {
						res.ConversationID = cid
					}
					if rid, ok := ctxArr[1].(string); ok {
						res.ResponseID = rid
					}
				}
			}

			if len(inner) > 4 {
				if contentArr, ok := inner[4].([]interface{}); ok && len(contentArr) > 0 {
					if msgItem, ok := contentArr[0].([]interface{}); ok && len(msgItem) > 1 {
						if contentList, ok := msgItem[1].([]interface{}); ok && len(contentList) > 0 {
							if text, ok := contentList[0].(string); ok {
								res.Text = text
							}
						}
						if choiceID, ok := msgItem[0].(string); ok {
							res.ChoiceID = choiceID
						}

						if len(msgItem) > 12 {
							if imgData, ok := msgItem[12].([]interface{}); ok && len(imgData) > 7 {
								if outerList, ok := imgData[7].([]interface{}); ok && len(outerList) > 0 {
									if innerList, ok := outerList[0].([]interface{}); ok {
										for _, imgObj := range innerList {
											if imgArr, ok := imgObj.([]interface{}); ok && len(imgArr) > 0 {
												if subArr, ok := imgArr[0].([]interface{}); ok && len(subArr) > 3 {
													if deepArr, ok := subArr[3].([]interface{}); ok && len(deepArr) > 3 {
														if imgURL, ok := deepArr[3].(string); ok {
															base := strings.Split(imgURL, "=")[0]
															imgURL = base + "=s0"
															
															// Deduplicate: only append if not already in res.Images
															isDup := false
															for _, existing := range res.Images {
																if existing == imgURL {
																	isDup = true
																	break
																}
															}
															if !isDup {
																res.Images = append(res.Images, imgURL)
															}
														}
													}
												}
											}
										}
									}
								}
							}
						}
						probeMusicURLs(msgItem, res, 0)
					}
				}
			}
		}
	}
}

// probeMusicURLs recursively probes nested data for music download URLs
func probeMusicURLs(data interface{}, res *GeminiResponse, depth int) {
	if depth > 20 || data == nil {
		return
	}
	switch v := data.(type) {
	case string:
		if len(v) > 20 && strings.HasPrefix(v, "http") &&
			strings.Contains(v, "contribution.usercontent.google.com") {
			found := false
			for _, m := range res.Music {
				if m.DownloadURL == v {
					found = true
					break
				}
			}
			if !found {
				track := MusicTrack{DownloadURL: v}
				if idx := strings.Index(v, "filename="); idx != -1 {
					fname := v[idx+9:]
					if ampIdx := strings.Index(fname, "&"); ampIdx != -1 {
						fname = fname[:ampIdx]
					}
					fname = strings.ReplaceAll(fname, "_", " ")
					fname = strings.TrimSuffix(fname, ".mp3")
					fname = strings.TrimSuffix(fname, ".mp4")
					track.Title = fname
				}
				res.Music = append(res.Music, track)
			}
		}
	case []interface{}:
		for _, item := range v {
			probeMusicURLs(item, res, depth+1)
		}
	case map[string]interface{}:
		for _, item := range v {
			probeMusicURLs(item, res, depth+1)
		}
	}
}

func (c *GeminiClient) DownloadFile(urlStr, savePath string) error {
	// Build a separate tls-client that does NOT follow redirects automatically.
	// We follow redirects manually so we can inject Cookie header on every hop.
	jar := tls_client.NewCookieJar()
	dlClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(60),
		tls_client.WithClientProfile(profiles.Chrome_152),
		tls_client.WithCookieJar(jar),
		tls_client.WithNotFollowRedirects(),
	)
	if err != nil {
		return fmt.Errorf("download client init failed: %w", err)
	}

	// Use CachedImageCookies (from extension bridge) if available, else RawCookies
	cookieStr := CachedImageCookies
	if cookieStr == "" {
		cookieStr = c.RawCookies
	}

	currentURL := urlStr
	for hops := 0; hops < 10; hops++ {
		req, err := http.NewRequest("GET", currentURL, nil)
		if err != nil {
			return err
		}

		req.Header[http.HeaderOrderKey] = chromeHeaderOrder
		req.Header[http.PHeaderOrderKey] = chromePseudoHeaderOrder

		req.Header.Set("User-Agent", CurrentChromeUA)
		req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
		req.Header.Set("sec-ch-ua", CurrentSecChUa)
		req.Header.Set("sec-ch-ua-mobile", "?0")
		req.Header.Set("sec-ch-ua-platform", CurrentPlatform)
		req.Header.Set("Referer", "https://gemini.google.com/")

		// Inject cookies on EVERY hop (this is the key fix for 403)
		if cookieStr != "" {
			req.Header.Set("Cookie", cookieStr)
		}

		resp, err := dlClient.Do(req)
		if err != nil {
			return fmt.Errorf("download request failed: %w", err)
		}

		// Handle redirects manually
		if resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 || resp.StatusCode == 307 || resp.StatusCode == 308 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if location == "" {
				return fmt.Errorf("redirect with no Location header (status %d)", resp.StatusCode)
			}
			log.Printf("🔀 Download redirect %d: %s → %s", resp.StatusCode, currentURL[:min(len(currentURL), 60)], location[:min(len(location), 80)])
			currentURL = location
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("download failed: Status %d at %s", resp.StatusCode, currentURL[:min(len(currentURL), 80)])
		}

		// Success — write to file
		dir := filepath.Dir(savePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			resp.Body.Close()
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		out, err := os.Create(savePath)
		if err != nil {
			resp.Body.Close()
			return err
		}

		_, err = io.Copy(out, resp.Body)
		resp.Body.Close()
		out.Close()
		if err != nil {
			return err
		}

		info, err := os.Stat(savePath)
		if err != nil {
			return err
		}
		if info.Size() < 1000 {
			return fmt.Errorf("file too small (%d bytes), likely failed download", info.Size())
		}

		return nil
	}

	return fmt.Errorf("too many redirects downloading %s", urlStr[:min(len(urlStr), 80)])
}

// ReloadSession re-reads the cookies file and marks the session to be re-initialized lazily on the next request
func (c *GeminiClient) ReloadSession() error {
	if err := c.loadCookies(); err != nil {
		return fmt.Errorf("failed to reload cookies: %v", err)
	}

	// Mark as uninitialized and invalidate session cache so the next request gets fresh tokens
	c.IsInitialized = false
	_ = os.Remove(c.getSessionCachePath())
	log.Println("♻️  Cookies reloaded in memory. Session marked for lazy re-initialization.")
	return nil
}
