package gemini

// MusicTrack holds metadata for a generated music track
type MusicTrack struct {
	Title       string `json:"title"`
	Album       string `json:"album,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Mood        string `json:"mood,omitempty"`
	DownloadURL string `json:"download_url"`
	LocalPath   string `json:"local_path,omitempty"`
	Duration    string `json:"duration,omitempty"`
}

// GeminiResponse represents the structured response from the API
type GeminiResponse struct {
	Text           string       `json:"text"`
	ConversationID string       `json:"conversation_id"`
	ResponseID     string       `json:"response_id"`
	ChoiceID       string       `json:"choice_id"`
	Images         []string     `json:"images"`
	Videos         []string     `json:"videos"`
	Music          []MusicTrack `json:"music,omitempty"`
	Elapsed        float64      `json:"elapsed"`
}

// ChatRequest represents the incoming request payload
type ChatRequest struct {
	Prompt  string `json:"prompt"`
	Message string `json:"message"`
	NewChat bool   `json:"new_chat"`
	UserID  string `json:"user_id"`
	Stream  bool   `json:"stream"`
	Model   string `json:"model,omitempty"`
}


