package factory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gocode-router/internal/config"
	"gocode-router/internal/provider"
	claudeProvider "gocode-router/internal/provider/claude"
	nvidiaProvider "gocode-router/internal/provider/nvidia"
	openaiProvider "gocode-router/internal/provider/openai"
)

const (
	defaultHTTPTimeout     = 60 * time.Second
	defaultDialTimeout     = 10 * time.Second
	defaultKeepAlive       = 30 * time.Second
	defaultIdleConnTimeout = 90 * time.Second
)

// RegisterConfiguredProviders constructs providers from configuration and stores them in the registry.
func RegisterConfiguredProviders(ctx context.Context, cfg config.Config, registry *provider.Registry) error {
	if registry == nil {
		return errors.New("registry must not be nil")
	}

	openAIClient := newHTTPClient(defaultHTTPTimeout)
	openAIProvider, err := openaiProvider.New("openai", cfg.Providers.OpenAI, openAIClient)
	if err != nil {
		return fmt.Errorf("initialise openai provider: %w", err)
	}
	if err := registry.RegisterProvider(ctx, openAIProvider, cfg.Providers.OpenAI.Aliases); err != nil {
		return fmt.Errorf("register openai provider: %w", err)
	}

	claudeClient := newHTTPClient(defaultHTTPTimeout)
	claudeProvider, err := claudeProvider.New("claude", cfg.Providers.Claude, claudeClient)
	if err != nil {
		return fmt.Errorf("initialise claude provider: %w", err)
	}
	if err := registry.RegisterProvider(ctx, claudeProvider, cfg.Providers.Claude.Aliases); err != nil {
		return fmt.Errorf("register claude provider: %w", err)
	}

	if cfg.Providers.NVIDIA != nil {
		nvidiaClient := newHTTPClient(defaultHTTPTimeout)
		nvidiaProvider, err := nvidiaProvider.New("nvidia", *cfg.Providers.NVIDIA, nvidiaClient)
		if err != nil {
			return fmt.Errorf("initialise nvidia provider: %w", err)
		}
		if err := registry.RegisterProvider(ctx, nvidiaProvider, cfg.Providers.NVIDIA.Aliases); err != nil {
			return fmt.Errorf("register nvidia provider: %w", err)
		}
	}

	return nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: defaultDialTimeout, KeepAlive: defaultKeepAlive}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
