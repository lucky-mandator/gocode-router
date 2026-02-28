package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gocode-router/internal/models"
)

// ErrUnknownModel indicates the requested model is not registered.
var ErrUnknownModel = errors.New("unknown model")

// ErrDuplicateModel indicates an attempt to register the same model twice.
var ErrDuplicateModel = errors.New("model already registered")

// ErrUnsupportedOperation indicates the provider cannot fulfill the requested action.
var ErrUnsupportedOperation = errors.New("unsupported provider operation")

// Provider defines the behaviour required to serve unified chat requests.
type Provider interface {
	Name() string
	ListModels(ctx context.Context) ([]models.Model, error)
	Chat(ctx context.Context, req models.UnifiedChatRequest) (*models.UnifiedChatResponse, error)
	Completion(ctx context.Context, req models.UnifiedCompletionRequest) (*models.UnifiedCompletionResponse, error)
}

type modelEntry struct {
	model    models.Model
	provider Provider
}

// Registry maintains a mapping of model IDs to providers.
type Registry struct {
	mu     sync.RWMutex
	models map[string]modelEntry
	byName map[string]Provider
}

// NewRegistry constructs an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		models: make(map[string]modelEntry),
		byName: make(map[string]Provider),
	}
}

// RegisterProvider adds the provider and its models to the registry, wiring optional aliases.
func (r *Registry) RegisterProvider(ctx context.Context, p Provider, aliases map[string]string) error {
	if p == nil {
		return errors.New("provider must not be nil")
	}

	modelsList, err := p.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("list models for provider %q: %w", p.Name(), err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byName[p.Name()]; exists {
		return fmt.Errorf("provider %q already registered", p.Name())
	}
	r.byName[p.Name()] = p

	for _, model := range modelsList {
		if _, exists := r.models[model.ID]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateModel, model.ID)
		}

		r.models[model.ID] = modelEntry{
			model:    model,
			provider: p,
		}
	}

	for alias, target := range aliases {
		if _, exists := r.models[alias]; exists {
			return fmt.Errorf("alias %q conflicts with existing model", alias)
		}

		targetEntry, ok := r.models[target]
		if !ok {
			return fmt.Errorf("alias %q references unknown model %q", alias, target)
		}

		r.models[alias] = targetEntry
	}

	return nil
}

// LookupModel returns the provider and metadata for a given model ID.
func (r *Registry) LookupModel(modelID string) (models.Model, Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.models[modelID]
	if !ok {
		return models.Model{}, nil, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
	}
	return entry.model, entry.provider, nil
}
