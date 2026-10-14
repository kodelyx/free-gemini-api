package gemini

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ModelConfig represents a Gemini model configuration
type ModelConfig struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	Description    string   `json:"description,omitempty"`
	ModeID         string   `json:"mode_id"`         // Internal hex hash (e.g. 56fdd199312815e2)
	HeaderTemplate string   `json:"header_template"` // JSPB template string
	ThinkingLevel  int      `json:"thinking_level,omitempty"`
	MinTier        string   `json:"min_tier"`        // "free" or "subscription"
	Aliases        []string `json:"aliases,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	IsDefault      bool     `json:"is_default"`
	Source         string   `json:"source"` // "builtin", "discovered", "custom"
}

// BuildHeaderValue constructs the x-goog-ext-525001261-jspb header value.
//
// Format verified via live CDP capture (2026-10-14):
//   [1,null,null,null,null,null,null,null,[caps],null,null,null,null,null,8,<thinkingLevel>,null,null,null,null]
//
// Thinking level sits at index 15:
//   Low=1, Medium=4, High=2
//
// For non-thinking models the classic short format is used:
//   [1,null,null,null,"<modeID>",null,null,0,[caps],null,null,2]
func (m *ModelConfig) BuildHeaderValue() string {
	if m.HeaderTemplate != "" {
		s := m.HeaderTemplate
		if strings.Contains(s, "%s") && strings.Contains(s, "%d") {
			lvl := m.ThinkingLevel
			if lvl <= 0 {
				lvl = 1
			}
			return fmt.Sprintf(s, m.ModeID, lvl)
		}
		if strings.Contains(s, "%d") {
			lvl := m.ThinkingLevel
			if lvl <= 0 {
				lvl = 1
			}
			return fmt.Sprintf(s, lvl)
		}
		if strings.Contains(s, "%s") {
			return fmt.Sprintf(s, m.ModeID)
		}
		return s
	}
	if m.ThinkingLevel > 0 {
		// Argon-style thinking format with ModeID at index 4 and thinking level at index 15
		return fmt.Sprintf(`[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`, m.ModeID, m.ThinkingLevel)
	}
	// Default non-thinking format with modeID
	return fmt.Sprintf(`[1,null,null,null,"%s",null,null,0,[4],null,null,2]`, m.ModeID)
}

// ModelRegistry manages the thread-safe collection of available models
type ModelRegistry struct {
	mu           sync.RWMutex
	models       map[string]*ModelConfig // ID (lowercase) -> ModelConfig
	aliasMap     map[string]string       // Alias (lowercase) -> canonical ID
	defaultModel string
	storagePath  string
}

var (
	globalModelRegistry *ModelRegistry
	registryOnce        sync.Once
)

// Default models catalog
var defaultBuiltinModels = []ModelConfig{
	{
		ID:             "gemini-3.5-flash-lite",
		DisplayName:    "Google Gemini 3.5 Flash-Lite",
		Description:    "Fastest answers — ultra low latency quick answers",
		ModeID:         "8c46e95b1a07cecc",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,0,[4],null,null,2]`,
		MinTier:        "free",
		Aliases:        []string{"3.5-flash-lite", "flash-lite", "gemini-flash-lite", "gemini-lite", "lite", "fbb127bbb056c959"},
		Capabilities:   []string{"chat", "stream", "vision", "tools", "fastest"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.8-flash",
		DisplayName:    "Google Gemini 3.8 Flash",
		Description:    "All-around help — balanced multi-modal flagship",
		ModeID:         "56fdd199312815e2",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,0,[4],null,null,2]`,
		MinTier:        "free",
		Aliases:        []string{"3.8-flash", "gemini-flash", "flash", "gemini-3.7-flash", "default"},
		Capabilities:   []string{"chat", "stream", "vision", "video", "tools"},
		IsDefault:      true,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.1-pro",
		DisplayName:    "Google Gemini 3.1 Pro",
		Description:    "Advanced reasoning — complex coding and multi-turn problem solving",
		ModeID:         "e6fa609c3fa255c0",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,0,[4],null,null,2]`,
		MinTier:        "subscription",
		Aliases:        []string{"3.1-pro", "gemini-pro", "pro", "gemini-3.0-pro", "gemini-2.5-pro", "advanced", "61530e79959ab139"},
		Capabilities:   []string{"chat", "stream", "vision", "tools", "complex_reasoning"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-4-argon",
		DisplayName:    "Google Gemini 4 Argon",
		Description:    "Complex tasks — next-generation frontier intelligence",
		ModeID:         "95a4c0abcba24364",
		// Default Argon without explicit thinking level (uses Low=1 by default per browser)
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,1,null,null,null,null]`,
		MinTier:        "free",
		Aliases:        []string{"4-argon", "gemini-argon", "argon", "gemini-4", "4", "f44d2958d1f0189a"},
		Capabilities:   []string{"chat", "stream", "vision", "tools", "complex_tasks", "frontier"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-4-argon-thinking-high",
		DisplayName:    "Google Gemini 4 Argon (Thinking High)",
		Description:    "Complex tasks with extra thorough thinking depth (High=2)",
		ModeID:         "95a4c0abcba24364",
		// Thinking level at index 15: High=2 (verified via CDP)
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  2,
		MinTier:        "free",
		Aliases:        []string{"argon-thinking-high", "argon-high", "thinking-high"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_tasks"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-4-argon-thinking-medium",
		DisplayName:    "Google Gemini 4 Argon (Thinking Medium)",
		Description:    "Complex tasks with balanced thinking depth (Medium=4)",
		ModeID:         "95a4c0abcba24364",
		// Thinking level at index 15: Medium=4 (verified via CDP)
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  4,
		MinTier:        "free",
		Aliases:        []string{"argon-thinking-medium", "argon-medium", "thinking-medium"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_tasks"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-4-argon-thinking-low",
		DisplayName:    "Google Gemini 4 Argon (Thinking Low)",
		Description:    "Complex tasks with quick and efficient thinking (Low=1)",
		ModeID:         "95a4c0abcba24364",
		// Thinking level at index 15: Low=1 (verified via CDP)
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  1,
		MinTier:        "free",
		Aliases:        []string{"argon-thinking-low", "argon-low", "thinking-low"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_tasks"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.8-flash-thinking-high",
		DisplayName:    "Google Gemini 3.8 Flash (Thinking High)",
		Description:    "Fast reasoning with extra thorough thinking depth (High=2)",
		ModeID:         "56fdd199312815e2",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  2,
		MinTier:        "free",
		Aliases:        []string{"flash-thinking-high", "flash-high"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.8-flash-thinking-medium",
		DisplayName:    "Google Gemini 3.8 Flash (Thinking Medium)",
		Description:    "Fast reasoning with balanced thinking depth (Medium=4)",
		ModeID:         "56fdd199312815e2",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  4,
		MinTier:        "free",
		Aliases:        []string{"flash-thinking-medium", "flash-medium"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.8-flash-thinking-low",
		DisplayName:    "Google Gemini 3.8 Flash (Thinking Low)",
		Description:    "Fast reasoning with quick thinking (Low=1)",
		ModeID:         "56fdd199312815e2",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  1,
		MinTier:        "free",
		Aliases:        []string{"flash-thinking-low", "flash-low"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.1-pro-thinking-high",
		DisplayName:    "Google Gemini 3.1 Pro (Thinking High)",
		Description:    "Pro reasoning with extra thorough thinking depth (High=2)",
		ModeID:         "e6fa609c3fa255c0",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  2,
		MinTier:        "subscription",
		Aliases:        []string{"pro-thinking-high", "pro-high"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_reasoning"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.1-pro-thinking-medium",
		DisplayName:    "Google Gemini 3.1 Pro (Thinking Medium)",
		Description:    "Pro reasoning with balanced thinking depth (Medium=4)",
		ModeID:         "e6fa609c3fa255c0",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  4,
		MinTier:        "subscription",
		Aliases:        []string{"pro-thinking-medium", "pro-medium"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_reasoning"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.1-pro-thinking-low",
		DisplayName:    "Google Gemini 3.1 Pro (Thinking Low)",
		Description:    "Pro reasoning with quick thinking (Low=1)",
		ModeID:         "e6fa609c3fa255c0",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,null,[4,5,6,8,16,19,20],null,null,null,null,null,8,%d,null,null,null,null]`,
		ThinkingLevel:  1,
		MinTier:        "subscription",
		Aliases:        []string{"pro-thinking-low", "pro-low"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code", "complex_reasoning"},
		IsDefault:      false,
		Source:         "builtin",
	},
	{
		ID:             "gemini-3.8-thinking",
		DisplayName:    "Google Gemini 3.8 Flash Thinking",
		Description:    "Deep reasoning with step-by-step thinking scratchpad trace",
		ModeID:         "56fdd199312815e2",
		HeaderTemplate: `[1,null,null,null,"%s",null,null,0,[4,5,6,8],null,null,1]`,
		ThinkingLevel:  1,
		MinTier:        "subscription",
		Aliases:        []string{"gemini-thinking", "flash-thinking", "thinking"},
		Capabilities:   []string{"chat", "stream", "thinking", "math", "code"},
		IsDefault:      false,
		Source:         "builtin",
	},
}

// GetModelRegistry returns the singleton ModelRegistry
func GetModelRegistry() *ModelRegistry {
	registryOnce.Do(func() {
		globalModelRegistry = &ModelRegistry{
			models:       make(map[string]*ModelConfig),
			aliasMap:     make(map[string]string),
			defaultModel: "gemini-3.8-flash",
			storagePath:  "models.json",
		}

		// Register built-in models
		for _, m := range defaultBuiltinModels {
			mCopy := m
			globalModelRegistry.registerInternal(&mCopy)
		}

		// Load custom or discovered models from disk
		globalModelRegistry.LoadFromFile()
	})
	return globalModelRegistry
}

func (r *ModelRegistry) registerInternal(m *ModelConfig) {
	key := strings.ToLower(strings.TrimSpace(m.ID))
	r.models[key] = m

	for _, alias := range m.Aliases {
		aKey := strings.ToLower(strings.TrimSpace(alias))
		if aKey != "" {
			r.aliasMap[aKey] = key
		}
	}

	if m.IsDefault {
		r.defaultModel = key
	}
}

// Register adds or updates a model in the registry and persists to disk
func (r *ModelRegistry) Register(m ModelConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.registerInternal(&m)
	_ = r.saveToFileInternal()
	log.Printf("🤖 [ModelRegistry] Registered model: %s (%s, ModeID: %s)", m.ID, m.DisplayName, m.ModeID)
}

// Unregister removes a model from the registry and updates disk persistence
func (r *ModelRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.ToLower(strings.TrimSpace(id))
	delete(r.models, key)
	for a, k := range r.aliasMap {
		if k == key {
			delete(r.aliasMap, a)
		}
	}
	_ = r.saveToFileInternal()
}

// Resolve looks up a model by ID or alias, falling back to default model
func (r *ModelRegistry) Resolve(name string) *ModelConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		if def, ok := r.models[r.defaultModel]; ok {
			return def
		}
		// Fallback to first available
		for _, m := range r.models {
			return m
		}
	}

	// 1. Direct ID match
	if m, ok := r.models[clean]; ok {
		return m
	}

	// 2. Direct Alias match
	if canonicalID, ok := r.aliasMap[clean]; ok {
		if m, ok := r.models[canonicalID]; ok {
			return m
		}
	}

	// 3. Substring matching for convenience
	if strings.Contains(clean, "argon") {
		if strings.Contains(clean, "high") || strings.Contains(clean, "3") {
			if m, ok := r.models["gemini-4-argon-thinking-high"]; ok {
				return m
			}
		}
		if strings.Contains(clean, "med") || strings.Contains(clean, "2") {
			if m, ok := r.models["gemini-4-argon-thinking-medium"]; ok {
				return m
			}
		}
		if strings.Contains(clean, "low") || strings.Contains(clean, "1") {
			if m, ok := r.models["gemini-4-argon-thinking-low"]; ok {
				return m
			}
		}
		if m, ok := r.models["gemini-4-argon"]; ok {
			return m
		}
	}
	if strings.Contains(clean, "lite") {
		if m, ok := r.models["gemini-3.5-flash-lite"]; ok {
			return m
		}
	}
	if strings.Contains(clean, "pro") {
		if canonicalID, ok := r.aliasMap["pro"]; ok {
			if m, ok := r.models[canonicalID]; ok {
				return m
			}
		}
	}
	if strings.Contains(clean, "thinking") {
		if canonicalID, ok := r.aliasMap["thinking"]; ok {
			if m, ok := r.models[canonicalID]; ok {
				return m
			}
		}
	}
	if strings.Contains(clean, "flash") {
		if m, ok := r.models["gemini-3.8-flash"]; ok {
			return m
		}
	}

	// Fallback to default
	if def, ok := r.models[r.defaultModel]; ok {
		return def
	}
	return &defaultBuiltinModels[0]
}

// Get looks up a model by ID or alias without fallback
func (r *ModelRegistry) Get(name string) (*ModelConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	clean := strings.ToLower(strings.TrimSpace(name))
	if m, ok := r.models[clean]; ok {
		return m, true
	}
	if canonicalID, ok := r.aliasMap[clean]; ok {
		if m, ok := r.models[canonicalID]; ok {
			return m, true
		}
	}
	return nil, false
}

// GetAll returns a list of all registered models
func (r *ModelRegistry) GetAll() []ModelConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]ModelConfig, 0, len(r.models))
	for _, m := range r.models {
		list = append(list, *m)
	}
	return list
}

// LoadFromFile loads custom and discovered models from models.json
func (r *ModelRegistry) LoadFromFile() {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.storagePath)
	if err != nil {
		altPath := filepath.Join("gemini", r.storagePath)
		if altData, altErr := os.ReadFile(altPath); altErr == nil {
			data = altData
		} else {
			return
		}
	}

	var customModels []ModelConfig
	if err := json.Unmarshal(data, &customModels); err != nil {
		log.Printf("⚠️ [ModelRegistry] Failed to parse %s: %v", r.storagePath, err)
		return
	}

	count := 0
	for _, m := range customModels {
		mCopy := m
		r.registerInternal(&mCopy)
		count++
	}
	if count > 0 {
		log.Printf("🤖 [ModelRegistry] Loaded %d custom/discovered models from %s", count, r.storagePath)
	}
}

func (r *ModelRegistry) saveToFileInternal() error {
	var toSave []ModelConfig
	for _, m := range r.models {
		// Save non-builtin or customized models
		if m.Source != "builtin" {
			toSave = append(toSave, *m)
		}
	}

	if len(toSave) == 0 {
		return nil
	}

	_ = os.MkdirAll(filepath.Dir(r.storagePath), 0755)
	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.storagePath, data, 0644)
}

// SaveToFile exports the current registry to a specific file path
func (r *ModelRegistry) SaveToFile(filePath string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []ModelConfig
	for _, m := range r.models {
		all = append(all, *m)
	}

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}
