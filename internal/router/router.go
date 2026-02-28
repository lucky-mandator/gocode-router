package router

import (
	"context"
	"fmt"

	"gocode-router/internal/models"
	"gocode-router/internal/provider"
)

// Router dispatches unified requests to the appropriate provider.
type Router struct {
	registry *provider.Registry
}

// New constructs a router backed by the provided registry.
func New(registry *provider.Registry) *Router {
	return &Router{
		registry: registry,
	}
}

// Chat routes a chat completion request to the configured provider.
func (r *Router) Chat(ctx context.Context, req models.UnifiedChatRequest) (*models.UnifiedChatResponse, models.Model, error) {
	modelInfo, providerImpl, err := r.registry.LookupModel(req.Model)
	if err != nil {
		return nil, models.Model{}, err
	}

	sanitisedReq := req
	sanitisedReq.Model = modelInfo.ID
	sanitisedReq.Options = cloneOptions(req.Options)

	resp, err := providerImpl.Chat(ctx, sanitisedReq)
	if err != nil {
		return nil, models.Model{}, fmt.Errorf("provider %s chat request: %w", providerImpl.Name(), err)
	}
	return resp, modelInfo, nil
}

// Completion routes a text completion request to the configured provider.
func (r *Router) Completion(ctx context.Context, req models.UnifiedCompletionRequest) (*models.UnifiedCompletionResponse, models.Model, error) {
	modelInfo, providerImpl, err := r.registry.LookupModel(req.Model)
	if err != nil {
		return nil, models.Model{}, err
	}

	sanitisedReq := req
	sanitisedReq.Model = modelInfo.ID
	sanitisedReq.Options = cloneOptions(req.Options)

	resp, err := providerImpl.Completion(ctx, sanitisedReq)
	if err != nil {
		return nil, models.Model{}, fmt.Errorf("provider %s completion request: %w", providerImpl.Name(), err)
	}
	return resp, modelInfo, nil
}

func cloneOptions(options map[string]any) map[string]any {
	if len(options) == 0 {
		return nil
	}
	out := make(map[string]any, len(options))
	for k, v := range options {
		out[k] = v
	}
	return out
}
