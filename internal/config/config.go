package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	apiStyleOpenAI = "openai"
	apiStyleClaude = "claude"
)

// Config represents the application configuration parsed from YAML.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Providers ProvidersConfig `yaml:"providers"`
}

// ServerConfig defines listener configuration.
type ServerConfig struct {
	Port int `yaml:"port"`
}

// ProvidersConfig catalogues configured upstream providers.
type ProvidersConfig struct {
	OpenAI ProviderConfig  `yaml:"openai"`
	Claude ProviderConfig  `yaml:"claude"`
	NVIDIA *ProviderConfig `yaml:"nvidia"`
}

// ProviderConfig captures authentication and routing info for a provider.
type ProviderConfig struct {
	APIKey  string            `yaml:"api_key"`
	BaseURL string            `yaml:"base_url"`
	Models  []ModelConfig     `yaml:"models"`
	Headers Headers           `yaml:"headers"`
	Aliases map[string]string `yaml:"aliases"`
}

// Headers contains additional HTTP headers to send with a provider request.
type Headers map[string]string

// ModelConfig describes a model exposed by a provider.
type ModelConfig struct {
	ID       string `yaml:"id"`
	APIStyle string `yaml:"api_style"`
}

// Load reads YAML configuration from disk and validates the result.
func Load(path string) (Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config file %q: %w", absPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config file %q: %w", absPath, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate performs strict sanity checks on the configuration.
func (c Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be a valid TCP port, got %d", c.Server.Port)
	}

	providers := map[string]ProviderConfig{
		"openai": c.Providers.OpenAI,
		"claude": c.Providers.Claude,
	}

	if c.Providers.NVIDIA != nil {
		providers["nvidia"] = *c.Providers.NVIDIA
	}

	for name, provider := range providers {
		if err := validateProvider(name, provider); err != nil {
			return err
		}
	}

	return nil
}

func validateProvider(name string, provider ProviderConfig) error {
	if strings.TrimSpace(provider.APIKey) == "" {
		return fmt.Errorf("provider %s: api_key must be provided", name)
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return fmt.Errorf("provider %s: base_url must be provided", name)
	}
	if len(provider.Models) == 0 {
		return fmt.Errorf("provider %s: at least one model must be configured", name)
	}

	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("provider %s: model id must not be empty", name)
		}
		if err := validateAPIStyle(name, model.APIStyle); err != nil {
			return err
		}
	}

	for headerKey := range provider.Headers {
		if !isCanonicalHTTPHeader(headerKey) {
			return fmt.Errorf("provider %s: header %q is not a valid canonical HTTP header", name, headerKey)
		}
	}

	for alias, target := range provider.Aliases {
		if strings.TrimSpace(alias) == "" {
			return fmt.Errorf("provider %s: alias name must not be empty", name)
		}
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("provider %s: alias %q target must not be empty", name, alias)
		}
	}

	return nil
}

func validateAPIStyle(providerName, style string) error {
	switch style {
	case apiStyleOpenAI, apiStyleClaude:
		return nil
	default:
		return fmt.Errorf("provider %s: model api_style %q must be one of %q or %q", providerName, style, apiStyleOpenAI, apiStyleClaude)
	}
}

func isCanonicalHTTPHeader(header string) bool {
	if header == "" {
		return false
	}

	for _, r := range header {
		if !(r == '-' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}
