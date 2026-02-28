package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gocode-router/internal/config"
	"gocode-router/internal/models"
	"gocode-router/internal/provider"
)

const (
	contentTypeJSON = "application/json"
	userAgent       = "gocode-router/0.1"
	apiVersion      = "2023-06-01"
)

// Provider implements Anthropic Claude API interactions.
type Provider struct {
	name     string
	apiKey   string
	baseURL  string
	headers  map[string]string
	client   *http.Client
	models   []models.Model
	messages string
}

// New constructs a Claude provider instance.
func New(name string, cfg config.ProviderConfig, client *http.Client) (*Provider, error) {
	if client == nil {
		return nil, errors.New("http client must not be nil")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		return nil, errors.New("base url must not be empty")
	}

	modelsList := make([]models.Model, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		if model.APIStyle != "claude" {
			return nil, fmt.Errorf("claude provider %q received model %q with unsupported api_style %q", name, model.ID, model.APIStyle)
		}
		modelsList = append(modelsList, models.Model{
			ID:       model.ID,
			Provider: name,
			APIStyle: model.APIStyle,
		})
	}

	return &Provider{
		name:     name,
		apiKey:   cfg.APIKey,
		baseURL:  baseURL,
		headers:  cfg.Headers,
		client:   client,
		models:   modelsList,
		messages: baseURL + "/v1/messages",
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) ListModels(ctx context.Context) ([]models.Model, error) {
	result := make([]models.Model, len(p.models))
	copy(result, p.models)
	return result, nil
}

func (p *Provider) Chat(ctx context.Context, req models.UnifiedChatRequest) (*models.UnifiedChatResponse, error) {
	if req.Stream {
		return nil, fmt.Errorf("streaming is not yet supported for provider %s: %w", p.name, provider.ErrUnsupportedOperation)
	}

	payload, err := buildMessagePayload(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, p.messages, payload)
	if err != nil {
		return nil, err
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claude chat request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return nil, parseAPIError(httpResp)
	}

	var providerResp messageResponse
	if err := decodeJSON(httpResp.Body, &providerResp); err != nil {
		return nil, err
	}

	return providerResp.toUnified()
}

func (p *Provider) Completion(ctx context.Context, req models.UnifiedCompletionRequest) (*models.UnifiedCompletionResponse, error) {
	return nil, fmt.Errorf("completions are not supported by provider %s: %w", p.name, provider.ErrUnsupportedOperation)
}

func (p *Provider) newRequest(ctx context.Context, method, url string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("construct request: %w", err)
	}

	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

type messagePayload struct {
	Model         string         `json:"model"`
	Messages      []message      `json:"messages"`
	System        string         `json:"system,omitempty"`
	MaxTokens     int            `json:"max_tokens"`
	Temperature   *float64       `json:"temperature,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	StopSequences []string       `json:"stop_sequences,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func buildMessagePayload(req models.UnifiedChatRequest) (messagePayload, error) {
	messages := make([]message, 0, len(req.Messages))
	var systemParts []string

	for _, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "user", "assistant":
			text := strings.TrimSpace(msg.Content)
			if text == "" {
				return messagePayload{}, errors.New("claude messages must not be empty")
			}
			messages = append(messages, message{
				Role: role,
				Content: []contentBlock{
					{Type: "text", Text: text},
				},
			})
		default:
			return messagePayload{}, fmt.Errorf("claude provider does not support role %q", msg.Role)
		}
	}

	if len(messages) == 0 {
		return messagePayload{}, errors.New("claude request requires at least one user message")
	}
	if messages[0].Role != "user" {
		return messagePayload{}, errors.New("claude conversation must start with a user message")
	}

	maxTokens, ok := extractInt(req.Options, "max_tokens")
	if !ok || maxTokens <= 0 {
		return messagePayload{}, errors.New("claude requests require a positive max_tokens value")
	}

	payload := messagePayload{
		Model:     req.Model,
		Messages:  messages,
		MaxTokens: maxTokens,
		Stream:    req.Stream,
	}

	if len(systemParts) > 0 {
		payload.System = strings.Join(systemParts, "\n\n")
	}
	if v, ok := extractFloat(req.Options, "temperature"); ok {
		payload.Temperature = &v
	}
	if v, ok := extractFloat(req.Options, "top_p"); ok {
		payload.TopP = &v
	}
	if stops, ok := extractStringSlice(req.Options, "stop"); ok {
		payload.StopSequences = stops
	}
	if metadata, ok := extractMap(req.Options, "metadata"); ok {
		payload.Metadata = metadata
	}

	return payload, nil
}

type messageResponse struct {
	ID         string         `json:"id"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Usage      usageBlock     `json:"usage"`
	StopReason string         `json:"stop_reason"`
	Error      *apiError      `json:"error,omitempty"`
}

type usageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (r messageResponse) toUnified() (*models.UnifiedChatResponse, error) {
	if len(r.Content) == 0 {
		return nil, errors.New("claude response missing content blocks")
	}

	text := strings.Builder{}
	for _, block := range r.Content {
		if block.Type != "text" {
			return nil, fmt.Errorf("claude returned unsupported content block type %q", block.Type)
		}
		text.WriteString(block.Text)
	}

	totalTokens := r.Usage.InputTokens + r.Usage.OutputTokens
	role := r.Role
	if role == "" {
		role = "assistant"
	}

	return &models.UnifiedChatResponse{
		ID: r.ID,
		Message: models.Message{
			Role:    role,
			Content: text.String(),
		},
		FinishReason: r.StopReason,
		Usage: models.Usage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      totalTokens,
		},
	}, nil
}

type apiErrorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func parseAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("upstream error status %d and failed to read body: %w", resp.StatusCode, err)
	}

	var apiErr apiErrorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
		return fmt.Errorf("claude error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
	}

	return fmt.Errorf("upstream error status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func extractFloat(options map[string]any, key string) (float64, bool) {
	if options == nil {
		return 0, false
	}
	if value, ok := options[key]; ok {
		switch v := value.(type) {
		case float64:
			return v, true
		case float32:
			return float64(v), true
		case json.Number:
			if f, err := v.Float64(); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func extractInt(options map[string]any, key string) (int, bool) {
	if options == nil {
		return 0, false
	}
	if value, ok := options[key]; ok {
		switch v := value.(type) {
		case int:
			return v, true
		case int64:
			return int(v), true
		case float64:
			return int(v), true
		case json.Number:
			if i, err := v.Int64(); err == nil {
				return int(i), true
			}
		}
	}
	return 0, false
}

func extractStringSlice(options map[string]any, key string) ([]string, bool) {
	if options == nil {
		return nil, false
	}
	value, ok := options[key]
	if !ok {
		return nil, false
	}

	switch v := value.(type) {
	case []string:
		return v, true
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, str)
		}
		return result, true
	}
	return nil, false
}

func extractMap(options map[string]any, key string) (map[string]any, bool) {
	if options == nil {
		return nil, false
	}
	if value, ok := options[key]; ok {
		if m, ok := value.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}
