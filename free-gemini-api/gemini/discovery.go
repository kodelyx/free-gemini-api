package gemini

import (
	"fmt"
	"io"
	"log"
	"path/filepath"
	"regexp"
	"strings"

	http "github.com/bogdanfinn/fhttp"
)

var (
	// Known mode hashes mapped to metadata
	knownModelSignatures = map[string]ModelConfig{
		"56fdd199312815e2": {
			ID:          "gemini-3.8-flash",
			DisplayName: "Google Gemini 3.8 Flash",
			ModeID:      "56fdd199312815e2",
			MinTier:     "free",
			Aliases:     []string{"gemini-flash", "flash"},
			Source:      "discovered",
		},
		"95a4c0abcba24364": {
			ID:          "gemini-4-argon",
			DisplayName: "Google Gemini 4 Argon",
			ModeID:      "95a4c0abcba24364",
			MinTier:     "free",
			Aliases:     []string{"4-argon", "argon", "gemini-argon"},
			Source:      "discovered",
		},
		"e6fa609c3fa255c0": {
			ID:          "gemini-3.1-pro",
			DisplayName: "Google Gemini 3.1 Pro",
			ModeID:      "e6fa609c3fa255c0",
			MinTier:     "subscription",
			Aliases:     []string{"gemini-pro", "pro", "advanced"},
			Source:      "discovered",
		},
		"8c46e95b1a07cecc": {
			ID:          "gemini-3.5-flash-lite",
			DisplayName: "Google Gemini 3.5 Flash-Lite",
			ModeID:      "8c46e95b1a07cecc",
			MinTier:     "free",
			Aliases:     []string{"3.5-flash-lite", "flash-lite", "gemini-flash-lite", "gemini-lite", "lite"},
			Source:      "discovered",
		},
		// Legacy / alternative hashes
		"61530e79959ab139": {
			ID:          "gemini-3.1-pro",
			DisplayName: "Google Gemini 3.1 Pro (Legacy)",
			ModeID:      "61530e79959ab139",
			MinTier:     "subscription",
			Aliases:     []string{"gemini-pro-legacy"},
			Source:      "discovered",
		},
		"f44d2958d1f0189a": {
			ID:          "gemini-4-argon",
			DisplayName: "Google Gemini 4 Argon (Legacy)",
			ModeID:      "f44d2958d1f0189a",
			MinTier:     "free",
			Aliases:     []string{"4-argon-legacy"},
			Source:      "discovered",
		},
		"fbb127bbb056c959": {
			ID:          "gemini-3.5-flash-lite",
			DisplayName: "Google Gemini 3.5 Flash-Lite (Legacy)",
			ModeID:      "fbb127bbb056c959",
			MinTier:     "free",
			Aliases:     []string{"flash-lite-legacy"},
			Source:      "discovered",
		},
	}

	// Regex to extract 16-character hexadecimal mode hashes from script tags or JSON payloads
	hexModeRegex = regexp.MustCompile(`["']([a-f0-9]{16})["']`)
)

// DetectAccountTier scans HTML payload and filename to detect if account has Gemini Advanced / Subscription
func DetectAccountTier(htmlContent string, cookieFileName string) string {
	lowerHTML := strings.ToLower(htmlContent)

	// Check explicit subscription signals in Google frontend payload
	// Note: explore-plan/gemini-advanced is an upgrade promo URL shown to free users
	if strings.Contains(lowerHTML, `"is_advanced":true`) ||
		strings.Contains(lowerHTML, `"is_subscriber":true`) ||
		(strings.Contains(lowerHTML, `gemini-advanced`) && !strings.Contains(lowerHTML, `explore-plan/gemini-advanced`)) ||
		strings.Contains(lowerHTML, `"subscriber":true`) {
		return "subscription"
	}

	// Filename fallback convention (e.g. account_pro.json, sub_account.json)
	base := strings.ToLower(filepath.Base(cookieFileName))
	if strings.Contains(base, "pro") || strings.Contains(base, "sub") || strings.Contains(base, "advanced") {
		return "subscription"
	}

	return "free"
}

// DiscoverModelsFromHTML parses Gemini web app HTML to discover model IDs and hashes
func DiscoverModelsFromHTML(htmlContent string) []ModelConfig {
	var discovered []ModelConfig
	seen := make(map[string]bool)

	// Search for all 16-hex hashes
	matches := hexModeRegex.FindAllStringSubmatch(htmlContent, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		hash := m[1]
		if seen[hash] {
			continue
		}

		if known, ok := knownModelSignatures[hash]; ok {
			seen[hash] = true
			discovered = append(discovered, known)
			continue
		}

		// If a new unidentified 16-hex hash is found inside model configuration context
		idx := strings.Index(htmlContent, hash)
		if idx != -1 {
			start := max(0, idx-100)
			end := min(len(htmlContent), idx+100)
			contextSnippet := strings.ToLower(htmlContent[start:end])

			if strings.Contains(contextSnippet, "model") ||
				strings.Contains(contextSnippet, "bard") ||
				strings.Contains(contextSnippet, "gemini") ||
				strings.Contains(contextSnippet, "assistant") {
				seen[hash] = true
				newModel := ModelConfig{
					ID:             fmt.Sprintf("gemini-discovered-%s", hash[:6]),
					DisplayName:    fmt.Sprintf("Google Gemini (Discovered %s)", hash[:6]),
					ModeID:         hash,
					HeaderTemplate: `[1,null,null,null,"%s",null,null,0,[4],null,null,2]`,
					MinTier:        "subscription",
					Source:         "discovered",
				}
				discovered = append(discovered, newModel)
			}
		}
	}

	return discovered
}

// DiscoverAccountModels connects to Gemini web using the client and discovers available models
func DiscoverAccountModels(client *GeminiClient) ([]ModelConfig, error) {
	if client == nil {
		return nil, fmt.Errorf("client is nil")
	}

	req, err := http.NewRequest("GET", "https://gemini.google.com/app", nil)
	if err != nil {
		return nil, err
	}

	req.Header[http.HeaderOrderKey] = chromeHeaderOrder
	req.Header[http.PHeaderOrderKey] = chromePseudoHeaderOrder
	req.Header.Set("User-Agent", CurrentChromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", "https://gemini.google.com/")

	resp, err := client.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	bodyStr := string(bodyBytes)

	// Update client account tier
	client.AccountTier = DetectAccountTier(bodyStr, client.cookiesFile)
	log.Printf("🔍 [Discovery] Account %s tier evaluated as: %s", filepath.Base(client.cookiesFile), client.AccountTier)

	// Discover models
	models := DiscoverModelsFromHTML(bodyStr)
	reg := GetModelRegistry()

	for _, m := range models {
		reg.Register(m)
		log.Printf("✨ [Discovery] Discovered and registered model: %s (%s, MinTier: %s)", m.ID, m.DisplayName, m.MinTier)
	}

	return models, nil
}
