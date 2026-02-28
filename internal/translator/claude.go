package translator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gocode-router/internal/models"
)

var (
	errClaudeEmptyModel      = errors.New("model must be provided")
	errClaudeEmptyMessages   = errors.New("at least one message is required")
	errClaudeInvalidRole     = errors.New("invalid role")
	errClaudeInvalidContent  = errors.New("invalid message content")
	errClaudeInvalidSystem   = errors.New("invalid system prompt")
	errClaudeUnsupportedStop = errors.New("unsupported stop sequences")
)

// ClaudeMessageRequest models the Anthropic Claude /v1/messages payload.
type ClaudeMessageRequest struct {
	Model         string
	MaxTokens     *int
	Messages      []ClaudeMessage
	System        []string
	Stream        bool
	Temperature   *float64
	TopP          *float64
	StopSequences []string
	Metadata      map[string]any
	Options       map[string]any
}

// UnmarshalJSON enforces validation and normalises fields.
func (r *ClaudeMessageRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model         string          `json:"model"`
		MaxTokens     *int            `json:"max_tokens"`
		Messages      []ClaudeMessage `json:"messages"`
		System        json.RawMessage `json:"system"`
		Stream        bool            `json:"stream"`
		Temperature   *float64        `json:"temperature"`
		TopP          *float64        `json:"top_p"`
		StopSequences json.RawMessage `json:"stop_sequences"`
		Metadata      map[string]any  `json:"metadata"`
	}

	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode claude request: %w", err)
	}

	systemPrompts, err := parseClaudeSystem(raw.System)
	if err != nil {
		return err
	}

	stopSequences, err := parseClaudeStops(raw.StopSequences)
	if err != nil {
		return err
	}

	r.Model = strings.TrimSpace(raw.Model)
	r.MaxTokens = raw.MaxTokens
	r.Messages = raw.Messages
	r.System = systemPrompts
	r.Stream = raw.Stream
	r.Temperature = raw.Temperature
	r.TopP = raw.TopP
	r.StopSequences = stopSequences
	r.Metadata = raw.Metadata
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
	if len(stopSequences) > 0 {
		r.Options["stop"] = stopSequences
	}
	if raw.Metadata != nil {
		r.Options["metadata"] = raw.Metadata
	}

	if err := r.validate(); err != nil {
		return err
	}

	return nil
}

func (r *ClaudeMessageRequest) validate() error {
	if r.Model == "" {
		return errClaudeEmptyModel
	}
	if len(r.Messages) == 0 {
		return errClaudeEmptyMessages
	}
	for i, msg := range r.Messages {
		if err := msg.validate(); err != nil {
			return fmt.Errorf("messages[%d]: %w", i, err)
		}
	}
	return nil
}

// ToUnified converts the Claude request into the canonical format.
func (r ClaudeMessageRequest) ToUnified() models.UnifiedChatRequest {
	msgs := make([]models.Message, 0, len(r.Messages)+len(r.System))

	for _, systemMsg := range r.System {
		if strings.TrimSpace(systemMsg) != "" {
			msgs = append(msgs, models.Message{
				Role:    "system",
				Content: systemMsg,
			})
		}
	}

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

// ClaudeMessage represents a single message in the request payload.
type ClaudeMessage struct {
	Role    string
	Content string
	Name    string
}

// UnmarshalJSON normalises the Claude message content structure.
func (m *ClaudeMessage) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Name    string          `json:"name"`
	}

	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode claude message: %w", err)
	}

	content, err := extractClaudeContent(raw.Content)
	if err != nil {
		return err
	}

	m.Role = strings.TrimSpace(raw.Role)
	m.Content = content
	m.Name = strings.TrimSpace(raw.Name)

	return m.validate()
}

func (m *ClaudeMessage) validate() error {
	switch m.Role {
	case "user", "assistant":
	default:
		return fmt.Errorf("%w: %s", errClaudeInvalidRole, m.Role)
	}

	if strings.TrimSpace(m.Content) == "" {
		return errClaudeInvalidContent
	}

	return nil
}

func parseClaudeSystem(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		s := strings.TrimSpace(single)
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}

	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		out := make([]string, 0, len(multiple))
		for _, item := range multiple {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}

	var singleBlock claudeSystemBlock
	if err := json.Unmarshal(raw, &singleBlock); err == nil && singleBlock.Type != "" {
		text, err := extractSystemBlock(singleBlock)
		if err != nil {
			return nil, err
		}
		if text == "" {
			return nil, nil
		}
		return []string{text}, nil
	}

	var blocks []claudeSystemBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := make([]string, 0, len(blocks))
		for _, block := range blocks {
			text, err := extractSystemBlock(block)
			if err != nil {
				return nil, err
			}
			if text == "" {
				continue
			}
			out = append(out, text)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}

	return nil, errClaudeInvalidSystem
}

func parseClaudeStops(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var stops []string
	if err := json.Unmarshal(raw, &stops); err != nil {
		return nil, errClaudeUnsupportedStop
	}

	out := make([]string, 0, len(stops))
	for _, stop := range stops {
		stop = strings.TrimSpace(stop)
		if stop == "" {
			return nil, errClaudeUnsupportedStop
		}
		out = append(out, stop)
	}
	return out, nil
}

func extractClaudeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errClaudeInvalidContent
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var builder strings.Builder
		for _, block := range blocks {
			if block.Type != "text" {
				return "", fmt.Errorf("%w: unsupported block type %q", errClaudeInvalidContent, block.Type)
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(strings.TrimSpace(block.Text))
		}
		result := strings.TrimSpace(builder.String())
		if result == "" {
			return "", errClaudeInvalidContent
		}
		return result, nil
	}

	return "", errClaudeInvalidContent
}

// ClaudeMessageResponse models the Anthropic response payload.
type ClaudeMessageResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []ClaudeTextBlock `json:"content"`
	StopReason string            `json:"stop_reason,omitempty"`
	Usage      ClaudeUsage       `json:"usage"`
	StopSeq    string            `json:"stop_sequence,omitempty"`
}

// ClaudeTextBlock represents a text content block in the response.
type ClaudeTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ClaudeUsage mirrors Anthropic usage format.
type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// FromUnifiedClaude converts the unified response to Anthropic format.
func FromUnifiedClaude(modelID string, resp *models.UnifiedChatResponse) ClaudeMessageResponse {
	role := resp.Message.Role
	if role == "" {
		role = "assistant"
	}

	contentText := resp.Message.Content
	if strings.TrimSpace(contentText) == "" {
		contentText = ""
	}

	return ClaudeMessageResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  role,
		Model: modelID,
		Content: []ClaudeTextBlock{
			{
				Type: "text",
				Text: contentText,
			},
		},
		StopReason: resp.FinishReason,
		Usage: ClaudeUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}
}

type claudeSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func extractSystemBlock(block claudeSystemBlock) (string, error) {
	if block.Type != "" && block.Type != "text" {
		return "", fmt.Errorf("%w: unsupported block type %q", errClaudeInvalidSystem, block.Type)
	}
	return strings.TrimSpace(block.Text), nil
}
