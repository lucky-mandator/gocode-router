package translator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gocode-router/internal/models"
)

var (
	errEmptyModel      = errors.New("model must be provided")
	errEmptyMessages   = errors.New("at least one message is required")
	errUnsupportedStop = errors.New("unsupported stop value")
	errInvalidRole     = errors.New("invalid role")
	errInvalidContent  = errors.New("invalid message content")
)

var allowedRoles = map[string]struct{}{
	"system":    {},
	"user":      {},
	"assistant": {},
	"tool":      {},
}

// ChatCompletionRequest models the OpenAI chat/completions request payload.
type ChatCompletionRequest struct {
	Model            string
	Messages         []ChatMessage
	Stream           bool
	MaxTokens        *int
	Temperature      *float64
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	Stop             []string
	ResponseFormat   map[string]any
	ToolsRaw         json.RawMessage
	ToolChoiceRaw    json.RawMessage
	LogitBias        map[string]float64
	Metadata         map[string]any
	User             string
	Options          map[string]any
}

// UnmarshalJSON implements custom parsing to enforce validation.
func (r *ChatCompletionRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model            string             `json:"model"`
		Messages         []ChatMessage      `json:"messages"`
		Stream           bool               `json:"stream"`
		MaxTokens        *int               `json:"max_tokens"`
		Temperature      *float64           `json:"temperature"`
		TopP             *float64           `json:"top_p"`
		FrequencyPenalty *float64           `json:"frequency_penalty"`
		PresencePenalty  *float64           `json:"presence_penalty"`
		Stop             json.RawMessage    `json:"stop"`
		ResponseFormat   map[string]any     `json:"response_format"`
		Tools            json.RawMessage    `json:"tools"`
		ToolChoice       json.RawMessage    `json:"tool_choice"`
		LogitBias        map[string]float64 `json:"logit_bias"`
		Metadata         map[string]any     `json:"metadata"`
		User             string             `json:"user"`
		Seed             json.RawMessage    `json:"seed"`
	}

	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode chat request: %w", err)
	}

	stopValues, err := parseStop(raw.Stop)
	if err != nil {
		return err
	}

	r.Model = strings.TrimSpace(raw.Model)
	r.Messages = raw.Messages
	r.Stream = raw.Stream
	r.MaxTokens = raw.MaxTokens
	r.Temperature = raw.Temperature
	r.TopP = raw.TopP
	r.FrequencyPenalty = raw.FrequencyPenalty
	r.PresencePenalty = raw.PresencePenalty
	r.Stop = stopValues
	r.ResponseFormat = raw.ResponseFormat
	r.ToolsRaw = raw.Tools
	r.ToolChoiceRaw = raw.ToolChoice
	r.LogitBias = raw.LogitBias
	r.Metadata = raw.Metadata
	r.User = raw.User

	r.Options = make(map[string]any)
	if raw.Temperature != nil {
		r.Options["temperature"] = *raw.Temperature
	}
	if raw.TopP != nil {
		r.Options["top_p"] = *raw.TopP
	}
	if raw.MaxTokens != nil {
		r.Options["max_tokens"] = *raw.MaxTokens
	}
	if raw.FrequencyPenalty != nil {
		r.Options["frequency_penalty"] = *raw.FrequencyPenalty
	}
	if raw.PresencePenalty != nil {
		r.Options["presence_penalty"] = *raw.PresencePenalty
	}
	if len(stopValues) > 0 {
		r.Options["stop"] = stopValues
	}
	if raw.ResponseFormat != nil {
		r.Options["response_format"] = raw.ResponseFormat
	}
	if len(raw.Tools) > 0 {
		r.Options["tools"] = json.RawMessage(raw.Tools)
	}
	if len(raw.ToolChoice) > 0 {
		r.Options["tool_choice"] = json.RawMessage(raw.ToolChoice)
	}
	if raw.LogitBias != nil {
		r.Options["logit_bias"] = raw.LogitBias
	}
	if raw.Metadata != nil {
		r.Options["metadata"] = raw.Metadata
	}
	if raw.User != "" {
		r.Options["user"] = raw.User
	}

	return r.validate()
}

func (r *ChatCompletionRequest) validate() error {
	if r.Model == "" {
		return errEmptyModel
	}
	if len(r.Messages) == 0 {
		return errEmptyMessages
	}
	for i, msg := range r.Messages {
		if err := msg.validate(); err != nil {
			return fmt.Errorf("message[%d]: %w", i, err)
		}
	}
	return nil
}

// ToUnified converts the OpenAI request into the canonical format.
func (r ChatCompletionRequest) ToUnified() models.UnifiedChatRequest {
	msgs := make([]models.Message, 0, len(r.Messages))
	for _, m := range r.Messages {
		msgs = append(msgs, models.Message{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}

	options := make(map[string]any, len(r.Options))
	for k, v := range r.Options {
		options[k] = v
	}

	return models.UnifiedChatRequest{
		Model:    r.Model,
		Messages: msgs,
		Stream:   r.Stream,
		Options:  options,
	}
}

// ChatMessage captures a single message within the chat request.
type ChatMessage struct {
	Role    string
	Content string
	Name    string
}

// UnmarshalJSON supports string and array-of-text content formats.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Name    string          `json:"name"`
	}

	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}

	content, err := extractMessageContent(raw.Content)
	if err != nil {
		return err
	}

	m.Role = strings.TrimSpace(raw.Role)
	m.Content = content
	m.Name = strings.TrimSpace(raw.Name)

	return m.validate()
}

func (m *ChatMessage) validate() error {
	if _, ok := allowedRoles[m.Role]; !ok {
		return fmt.Errorf("%w: %s", errInvalidRole, m.Role)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("%w: message content must not be empty", errInvalidContent)
	}
	return nil
}

func extractMessageContent(raw json.RawMessage) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("%w: missing content", errInvalidContent)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var segments []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &segments); err == nil {
		var builder strings.Builder
		for _, segment := range segments {
			if segment.Type != "text" {
				return "", fmt.Errorf("%w: segment type %q not supported", errInvalidContent, segment.Type)
			}
			builder.WriteString(segment.Text)
		}
		return builder.String(), nil
	}

	return "", fmt.Errorf("%w: unsupported content structure", errInvalidContent)
}

func parseStop(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil, errUnsupportedStop
		}
		return []string{single}, nil
	}

	var multi []string
	if err := json.Unmarshal(raw, &multi); err == nil {
		out := make([]string, 0, len(multi))
		for _, item := range multi {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, errUnsupportedStop
			}
			out = append(out, item)
		}
		return out, nil
	}
	return nil, errUnsupportedStop
}

// ChatCompletionResponse models the OpenAI-compatible chat response.
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *OpenAIUsage `json:"usage,omitempty"`
}

// ChatChoice represents a single choice in the response payload.
type ChatChoice struct {
	Index        int          `json:"index"`
	Message      ChatMessage  `json:"message"`
	FinishReason string       `json:"finish_reason,omitempty"`
	Logprobs     any          `json:"logprobs,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
}

// OpenAIUsage mirrors the token usage block in OpenAI responses.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FromUnifiedChat constructs the OpenAI response shape from the unified data.
func FromUnifiedChat(modelID string, createdUnix int64, resp *models.UnifiedChatResponse) ChatCompletionResponse {
	choice := ChatChoice{
		Index: 0,
		Message: ChatMessage{
			Role:    resp.Message.Role,
			Content: resp.Message.Content,
			Name:    resp.Message.Name,
		},
		FinishReason: resp.FinishReason,
	}

	var usage *OpenAIUsage
	if resp.Usage.TotalTokens != 0 || resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 {
		usage = &OpenAIUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: createdUnix,
		Model:   modelID,
		Choices: []ChatChoice{choice},
		Usage:   usage,
	}
}

// CompletionRequest models the legacy OpenAI text completions request payload.
type CompletionRequest struct {
	Model       string
	Prompt      string
	Stream      bool
	MaxTokens   *int
	Temperature *float64
	TopP        *float64
	Options     map[string]any
}

// UnmarshalJSON performs strict validation for completion requests.
func (r *CompletionRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model       string          `json:"model"`
		Prompt      json.RawMessage `json:"prompt"`
		Stream      bool            `json:"stream"`
		MaxTokens   *int            `json:"max_tokens"`
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
	}

	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode completion request: %w", err)
	}

	prompt, err := extractPrompt(raw.Prompt)
	if err != nil {
		return err
	}

	r.Model = strings.TrimSpace(raw.Model)
	r.Prompt = prompt
	r.Stream = raw.Stream
	r.MaxTokens = raw.MaxTokens
	r.Temperature = raw.Temperature
	r.TopP = raw.TopP
	r.Options = make(map[string]any)

	if raw.MaxTokens != nil {
		r.Options["max_tokens"] = *raw.MaxTokens
	}
	if raw.Temperature != nil {
		r.Options["temperature"] = *raw.Temperature
	}
	if raw.TopP != nil {
		r.Options["top_p"] = *raw.TopP
	}

	if r.Model == "" {
		return errEmptyModel
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return errors.New("prompt must not be empty")
	}

	return nil
}

// ToUnified converts the completion request into unified form.
func (r CompletionRequest) ToUnified() models.UnifiedCompletionRequest {
	options := make(map[string]any, len(r.Options))
	for k, v := range r.Options {
		options[k] = v
	}
	return models.UnifiedCompletionRequest{
		Model:       r.Model,
		Prompt:      r.Prompt,
		Stream:      r.Stream,
		MaxTokens:   firstOrDefaultInt(r.MaxTokens),
		Temperature: firstOrDefaultFloat(r.Temperature),
		Options:     options,
	}
}

// CompletionResponse models the OpenAI completion response payload.
type CompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   *OpenAIUsage       `json:"usage,omitempty"`
}

// CompletionChoice represents a single completion choice.
type CompletionChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason,omitempty"`
	Logprobs     any    `json:"logprobs,omitempty"`
}

// FromUnifiedCompletion converts unified completion data to OpenAI shape.
func FromUnifiedCompletion(modelID string, createdUnix int64, resp *models.UnifiedCompletionResponse) CompletionResponse {
	var usage *OpenAIUsage
	if resp.Usage.TotalTokens != 0 || resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 {
		usage = &OpenAIUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return CompletionResponse{
		ID:      resp.ID,
		Object:  "text_completion",
		Created: createdUnix,
		Model:   modelID,
		Choices: []CompletionChoice{
			{
				Text:         resp.Text,
				Index:        0,
				FinishReason: resp.FinishReason,
			},
		},
		Usage: usage,
	}
}

func extractPrompt(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("prompt is required")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var parts []string
	if err := json.Unmarshal(raw, &parts); err == nil {
		return strings.Join(parts, "\n"), nil
	}

	return "", errors.New("unsupported prompt type")
}

func firstOrDefaultInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func firstOrDefaultFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
