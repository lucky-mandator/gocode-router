package models

// Message represents a single conversational message in the unified schema.
type Message struct {
	Role    string
	Content string
	Name    string
}

// UnifiedChatRequest is the canonical representation of a chat completion.
type UnifiedChatRequest struct {
	Model    string
	Messages []Message
	Stream   bool
	Options  map[string]any
}

// UnifiedChatResponse captures a provider response in the unified schema.
type UnifiedChatResponse struct {
	Message      Message
	Usage        Usage
	FinishReason string
	ID           string
}

// UnifiedCompletionRequest represents a text completion style request.
type UnifiedCompletionRequest struct {
	Model       string
	Prompt      string
	Stream      bool
	MaxTokens   int
	Temperature float64
	Options     map[string]any
}

// UnifiedCompletionResponse captures a completion-style response.
type UnifiedCompletionResponse struct {
	Text         string
	Usage        Usage
	FinishReason string
	ID           string
}

// Usage records token accounting information.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Model identifies a known model with provider metadata.
type Model struct {
	ID       string
	Provider string
	APIStyle string
}
