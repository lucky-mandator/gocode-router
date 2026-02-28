package openai

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
)

// Provider implements the Provider interface for OpenAI-compatible APIs.
type Provider struct {
	name      string
	apiKey    string
	baseURL   string
	headers   map[string]string
	client    *http.Client
	models    []models.Model
	chatURL   string
	legacyURL string
}

// New creates a new OpenAI provider.
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
		if model.APIStyle != "openai" {
			return nil, fmt.Errorf("openai provider %q received model %q with unsupported api_style %q", name, model.ID, model.APIStyle)
		}
		modelsList = append(modelsList, models.Model{
			ID:       model.ID,
			Provider: name,
			APIStyle: model.APIStyle,
		})
	}

	return &Provider{
		name:      name,
		apiKey:    cfg.APIKey,
		baseURL:   baseURL,
		headers:   cfg.Headers,
		client:    client,
		models:    modelsList,
		chatURL:   baseURL + "/chat/completions",
		legacyURL: baseURL + "/completions",
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

	payload, err := buildChatPayload(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, p.chatURL, payload)
	if err != nil {
		return nil, err
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai chat request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return nil, parseAPIError(httpResp)
	}

	var providerResp chatResponse
	if err := decodeJSON(httpResp.Body, &providerResp); err != nil {
		return nil, err
	}

	return providerResp.toUnified()
}

func (p *Provider) Completion(ctx context.Context, req models.UnifiedCompletionRequest) (*models.UnifiedCompletionResponse, error) {
	if req.Stream {
		return nil, fmt.Errorf("streaming is not yet supported for provider %s: %w", p.name, provider.ErrUnsupportedOperation)
	}

	payload, err := buildCompletionPayload(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, p.legacyURL, payload)
	if err != nil {
		return nil, err
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai completion request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 400 {
		return nil, parseAPIError(httpResp)
	}

	var providerResp completionResponse
	if err := decodeJSON(httpResp.Body, &providerResp); err != nil {
		return nil, err
	}

	return providerResp.toUnified()
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
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	return req, nil
}

type chatPayload struct {
	Model            string             `json:"model"`
	Messages         []openAIMessage    `json:"messages"`
	Stream           bool               `json:"stream,omitempty"`
	MaxTokens        *int               `json:"max_tokens,omitempty"`
	Temperature      *float64           `json:"temperature,omitempty"`
	TopP             *float64           `json:"top_p,omitempty"`
	FrequencyPenalty *float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64           `json:"presence_penalty,omitempty"`
	Stop             []string           `json:"stop,omitempty"`
	ResponseFormat   map[string]any     `json:"response_format,omitempty"`
	Tools            json.RawMessage    `json:"tools,omitempty"`
	ToolChoice       json.RawMessage    `json:"tool_choice,omitempty"`
	LogitBias        map[string]float64 `json:"logit_bias,omitempty"`
	Metadata         map[string]any     `json:"metadata,omitempty"`
	User             string             `json:"user,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

func buildChatPayload(req models.UnifiedChatRequest) (chatPayload, error) {
	messages := make([]openAIMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if strings.TrimSpace(msg.Content) == "" {
			return chatPayload{}, errors.New("message content must not be empty")
		}
		messages = append(messages, openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
			Name:    msg.Name,
		})
	}

	payload := chatPayload{
		Model:    req.Model,
		Messages: messages,
		Stream:   req.Stream,
	}

	if v, ok := extractInt(req.Options, "max_tokens"); ok {
		payload.MaxTokens = &v
	}
	if v, ok := extractFloat(req.Options, "temperature"); ok {
		payload.Temperature = &v
	}
	if v, ok := extractFloat(req.Options, "top_p"); ok {
		payload.TopP = &v
	}
	if v, ok := extractFloat(req.Options, "frequency_penalty"); ok {
		payload.FrequencyPenalty = &v
	}
	if v, ok := extractFloat(req.Options, "presence_penalty"); ok {
		payload.PresencePenalty = &v
	}
	if stop, ok := extractStringSlice(req.Options, "stop"); ok {
		payload.Stop = stop
	}
	if responseFormat, ok := extractMap(req.Options, "response_format"); ok {
		payload.ResponseFormat = responseFormat
	}
	if tools, ok := extractRaw(req.Options, "tools"); ok {
		payload.Tools = tools
	}
	if toolChoice, ok := extractRaw(req.Options, "tool_choice"); ok {
		payload.ToolChoice = toolChoice
	}
	if logitBias, ok := extractLogitBias(req.Options); ok {
		payload.LogitBias = logitBias
	}
	if metadata, ok := extractMap(req.Options, "metadata"); ok {
		payload.Metadata = metadata
	}
	if user, ok := extractString(req.Options, "user"); ok {
		payload.User = user
	}

	return payload, nil
}

type chatResponse struct {
	ID      string          `json:"id"`
	Choices []chatChoice    `json:"choices"`
	Usage   *usageBlock     `json:"usage,omitempty"`
	Error   *apiErrorObject `json:"error,omitempty"`
}

type chatChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type usageBlock struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (r chatResponse) toUnified() (*models.UnifiedChatResponse, error) {
	if len(r.Choices) == 0 {
		return nil, errors.New("openai response did not include choices")
	}

	choice := r.Choices[0]
	return &models.UnifiedChatResponse{
		ID: r.ID,
		Message: models.Message{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
			Name:    choice.Message.Name,
		},
		FinishReason: choice.FinishReason,
		Usage: models.Usage{
			PromptTokens:     valueOrZero(r.Usage, func(u *usageBlock) int { return u.PromptTokens }),
			CompletionTokens: valueOrZero(r.Usage, func(u *usageBlock) int { return u.CompletionTokens }),
			TotalTokens:      valueOrZero(r.Usage, func(u *usageBlock) int { return u.TotalTokens }),
		},
	}, nil
}

type completionPayload struct {
	Model       string             `json:"model"`
	Prompt      string             `json:"prompt"`
	Stream      bool               `json:"stream,omitempty"`
	MaxTokens   *int               `json:"max_tokens,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stop        []string           `json:"stop,omitempty"`
	LogitBias   map[string]float64 `json:"logit_bias,omitempty"`
	User        string             `json:"user,omitempty"`
}

func buildCompletionPayload(req models.UnifiedCompletionRequest) (completionPayload, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return completionPayload{}, errors.New("prompt must not be empty")
	}
	payload := completionPayload{
		Model:  req.Model,
		Prompt: req.Prompt,
		Stream: req.Stream,
	}

	if req.MaxTokens > 0 {
		v := req.MaxTokens
		payload.MaxTokens = &v
	}
	if req.Temperature != 0 {
		v := req.Temperature
		payload.Temperature = &v
	}
	if v, ok := extractFloat(req.Options, "top_p"); ok {
		payload.TopP = &v
	}
	if stop, ok := extractStringSlice(req.Options, "stop"); ok {
		payload.Stop = stop
	}
	if logitBias, ok := extractLogitBias(req.Options); ok {
		payload.LogitBias = logitBias
	}
	if user, ok := extractString(req.Options, "user"); ok {
		payload.User = user
	}

	return payload, nil
}

type completionResponse struct {
	ID      string             `json:"id"`
	Choices []completionChoice `json:"choices"`
	Usage   *usageBlock        `json:"usage,omitempty"`
	Error   *apiErrorObject    `json:"error,omitempty"`
}

type completionChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason"`
}

func (r completionResponse) toUnified() (*models.UnifiedCompletionResponse, error) {
	if len(r.Choices) == 0 {
		return nil, errors.New("openai completion response did not include choices")
	}

	choice := r.Choices[0]
	return &models.UnifiedCompletionResponse{
		ID:           r.ID,
		Text:         choice.Text,
		FinishReason: choice.FinishReason,
		Usage: models.Usage{
			PromptTokens:     valueOrZero(r.Usage, func(u *usageBlock) int { return u.PromptTokens }),
			CompletionTokens: valueOrZero(r.Usage, func(u *usageBlock) int { return u.CompletionTokens }),
			TotalTokens:      valueOrZero(r.Usage, func(u *usageBlock) int { return u.TotalTokens }),
		},
	}, nil
}

type apiErrorResponse struct {
	Error apiErrorObject `json:"error"`
}

type apiErrorObject struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func parseAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("upstream error status %d and failed to read body: %w", resp.StatusCode, err)
	}

	var apiErr apiErrorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
		return fmt.Errorf("openai error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
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

func extractString(options map[string]any, key string) (string, bool) {
	if options == nil {
		return "", false
	}
	if value, ok := options[key]; ok {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
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

func extractRaw(options map[string]any, key string) (json.RawMessage, bool) {
	if options == nil {
		return nil, false
	}
	if value, ok := options[key]; ok {
		switch v := value.(type) {
		case json.RawMessage:
			return v, true
		case []byte:
			return json.RawMessage(v), true
		case string:
			return json.RawMessage(v), true
		}
	}
	return nil, false
}

func extractLogitBias(options map[string]any) (map[string]float64, bool) {
	if options == nil {
		return nil, false
	}
	value, ok := options["logit_bias"]
	if !ok {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]float64:
		return v, true
	case map[string]any:
		out := make(map[string]float64, len(v))
		for key, rawVal := range v {
			switch val := rawVal.(type) {
			case float64:
				out[key] = val
			case float32:
				out[key] = float64(val)
			case json.Number:
				if f, err := val.Float64(); err == nil {
					out[key] = f
				} else {
					return nil, false
				}
			default:
				return nil, false
			}
		}
		return out, true
	}
	return nil, false
}

func valueOrZero[T any, R any](ptr *T, getter func(*T) R) R {
	var zero R
	if ptr == nil {
		return zero
	}
	return getter(ptr)
}
