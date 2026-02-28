package nvidia

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gocode-router/internal/config"
	"gocode-router/internal/models"
	"gocode-router/internal/provider"
	claudeProvider "gocode-router/internal/provider/claude"
	openaiProvider "gocode-router/internal/provider/openai"
)

const (
	apiStyleOpenAI = "openai"
	apiStyleClaude = "claude"
)

// Provider implements NVIDIA's multi-protocol routing.
type Provider struct {
	name        string
	models      []models.Model
	modelStyles map[string]string

	openaiAdapter *openaiProvider.Provider
	claudeAdapter *claudeProvider.Provider
}

// New constructs a provider that delegates to protocol-specific adapters.
func New(name string, cfg config.ProviderConfig, client *http.Client) (*Provider, error) {
	if client == nil {
		return nil, errors.New("http client must not be nil")
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		return nil, errors.New("base url must not be empty")
	}

	var (
		openaiModels []config.ModelConfig
		claudeModels []config.ModelConfig
		allModels    []models.Model
		modelStyles  = make(map[string]string)
	)

	for _, model := range cfg.Models {
		style := strings.TrimSpace(strings.ToLower(model.APIStyle))

		// Track for routing.
		allModels = append(allModels, models.Model{
			ID:       model.ID,
			Provider: name,
			APIStyle: style,
		})
		modelStyles[model.ID] = style

		switch style {
		case apiStyleOpenAI:
			openaiModels = append(openaiModels, model)
		case apiStyleClaude:
			claudeModels = append(claudeModels, model)
		default:
			return nil, fmt.Errorf("model %s: unsupported api_style %q", model.ID, model.APIStyle)
		}
	}

	p := &Provider{
		name:        name,
		models:      allModels,
		modelStyles: modelStyles,
	}

	if len(openaiModels) > 0 {
		openaiCfg := cfg
		openaiCfg.BaseURL = baseURL
		openaiCfg.Models = openaiModels

		adapter, err := openaiProvider.New(name, openaiCfg, client)
		if err != nil {
			return nil, fmt.Errorf("initialize openai adapter: %w", err)
		}
		p.openaiAdapter = adapter
	}

	if len(claudeModels) > 0 {
		claudeCfg := cfg
		claudeCfg.BaseURL = baseURL
		claudeCfg.Models = claudeModels

		adapter, err := claudeProvider.New(name, claudeCfg, client)
		if err != nil {
			return nil, fmt.Errorf("initialize claude adapter: %w", err)
		}
		p.claudeAdapter = adapter
	}

	return p, nil
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
	style, ok := p.modelStyles[req.Model]
	if !ok {
		return nil, fmt.Errorf("%w: %s", provider.ErrUnknownModel, req.Model)
	}

	switch style {
	case apiStyleOpenAI:
		if p.openaiAdapter == nil {
			return nil, fmt.Errorf("model %s configured as openai style but adapter missing", req.Model)
		}
		return p.openaiAdapter.Chat(ctx, req)
	case apiStyleClaude:
		if p.claudeAdapter == nil {
			return nil, fmt.Errorf("model %s configured as claude style but adapter missing", req.Model)
		}
		return p.claudeAdapter.Chat(ctx, req)
	default:
		return nil, fmt.Errorf("model %s has unsupported api style %q", req.Model, style)
	}
}

func (p *Provider) Completion(ctx context.Context, req models.UnifiedCompletionRequest) (*models.UnifiedCompletionResponse, error) {
	style, ok := p.modelStyles[req.Model]
	if !ok {
		return nil, fmt.Errorf("%w: %s", provider.ErrUnknownModel, req.Model)
	}

	switch style {
	case apiStyleOpenAI:
		if p.openaiAdapter == nil {
			return nil, fmt.Errorf("model %s configured as openai style but adapter missing", req.Model)
		}
		return p.openaiAdapter.Completion(ctx, req)
	case apiStyleClaude:
		return nil, fmt.Errorf("model %s uses claude api style which does not support completions: %w", req.Model, provider.ErrUnsupportedOperation)
	default:
		return nil, fmt.Errorf("model %s has unsupported api style %q", req.Model, style)
	}
}
