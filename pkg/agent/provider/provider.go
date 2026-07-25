package provider

import (
	"context"
	"fmt"
	"strings"
)

type Provider interface {
	Name() string
	ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

type StreamingProvider interface {
	Provider
	ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error)
}

type WebSearchProvider interface {
	WebSearch(ctx context.Context, query string, maxResults int) (*WebSearchResponse, error)
}

type WebSearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type WebSearchResponse struct {
	Results []WebSearchResult `json:"results,omitempty"`
	Summary string            `json:"summary,omitempty"`
}

type ProviderConfig struct {
	Provider      string `yaml:"provider" config:"provider"`
	BaseURL       string `yaml:"base_url" config:"base_url"`
	APIKey        string `yaml:"api_key"  config:"api_key"`
	Model         string `yaml:"model"    config:"model"`
	Proxy         string `yaml:"proxy"    config:"proxy"`
	Timeout       int    `yaml:"timeout"  config:"timeout"`
	Images        *bool  `yaml:"images,omitempty" config:"images"`
	MaxTokens     int    `yaml:"max_tokens,omitempty" config:"max_tokens"`
	ContextWindow int    `yaml:"context_window,omitempty" config:"context_window"`
}

func NormalizeProvider(name string) string {
	if strings.EqualFold(name, "anthropic") {
		return "anthropic"
	}
	return "openai"
}

func Resolve(cfg *ProviderConfig) (*ProviderConfig, error) {
	resolved := *cfg
	if resolved.MaxTokens < 0 {
		return nil, fmt.Errorf("max_tokens must be zero or positive")
	}
	if resolved.ContextWindow < 0 {
		return nil, fmt.Errorf("context_window must be zero or positive")
	}

	if resolved.Provider == "" {
		if resolved.BaseURL != "" {
			resolved.Provider = InferFromBaseURL(resolved.BaseURL)
		} else {
			resolved.Provider = "openai"
		}
	}
	resolved.Provider = NormalizeProvider(resolved.Provider)

	if resolved.BaseURL == "" {
		switch resolved.Provider {
		case "anthropic":
			resolved.BaseURL = "https://api.anthropic.com/v1"
		default:
			resolved.BaseURL = "https://api.openai.com/v1"
		}
	}

	if resolved.APIKey == "" {
		return nil, fmt.Errorf("no API key: set --api-key, llm.api_key, or AISCAN_API_KEY")
	}

	if resolved.Timeout <= 0 {
		resolved.Timeout = 120
	}

	if resolved.Images == nil {
		v := inferImageSupport(resolved.Provider, resolved.Model)
		resolved.Images = &v
	}

	return &resolved, nil
}

func NewProvider(cfg *ProviderConfig) (Provider, error) {
	resolved, err := Resolve(cfg)
	if err != nil {
		return nil, err
	}
	return NewProviderFromResolved(resolved)
}

// inferImageSupport guesses whether a provider+model combination accepts
// image content parts based on the provider type and model name heuristics.
// Defaults to true for known provider types (anthropic/openai) and falls
// back to model-name heuristics for unknown providers.
func inferImageSupport(provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))

	if isKnownMultimodalModel(m) {
		return true
	}
	if isKnownTextOnlyModel(m) {
		return false
	}

	switch p {
	case "anthropic":
		return true
	}

	return false
}

// InferFromBaseURL guesses the wire protocol from the base URL when --provider
// is not set. The official Anthropic endpoint is unambiguous, so it is detected
// directly. Everything else — including custom third-party gateways — speaks the
// OpenAI protocol in the common case, so "openai" stays the default. The
// protocol genuinely cannot be sniffed for an arbitrary gateway (a gateway may
// serve, e.g., glm models over the Anthropic protocol), so a wrong guess is
// caught later as an actionable 404 from the provider (see hint404), not a
// silent failure.
func InferFromBaseURL(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "anthropic.com") {
		return "anthropic"
	}
	return "openai"
}

func NewProviderFromResolved(cfg *ProviderConfig) (Provider, error) {
	if strings.ToLower(cfg.Provider) == "anthropic" {
		return NewAnthropicProvider(cfg)
	}
	return NewOpenAIProvider(cfg)
}

// Model capability registry extracted from pi's models.generated.ts.
// Check order: multimodal keywords first (more specific), then text-only
// keywords (broader families). Unknown models fall through to provider defaults.

var knownMultimodalKeywords = []string{
	"claude",
	"gemini",
	"pixtral",
	"gpt-4o",
	"4o",
	"gpt-4-turbo",
	"nova-lite",
	"nova-pro",
	"nova-2",
	"vision",
	"vl",
	"multimodal",
}

var knownTextOnlyKeywords = []string{
	"deepseek",
	"llama",
	"qwen",
	"glm",
	"mistral",
	"mixtral",
	"ministral",
	"magistral",
	"minimax",
	"grok",
	"mimo",
	"nemotron",
	"codestral",
	"devstral",
	"kimi",
	"gpt-oss",
	"command-r",
	"jamba",
	"solar",
	"nova-micro",
	"o3-mini",
	"seed-",
	"step-",
}

func isKnownMultimodalModel(model string) bool {
	for _, kw := range knownMultimodalKeywords {
		if strings.Contains(model, kw) {
			return true
		}
	}
	return false
}

func isKnownTextOnlyModel(model string) bool {
	for _, kw := range knownTextOnlyKeywords {
		if strings.Contains(model, kw) {
			return true
		}
	}
	return false
}
