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

func Resolve(cfg *ProviderConfig) (*ProviderConfig, error) {
	resolved := *cfg
	if resolved.MaxTokens < 0 {
		return nil, fmt.Errorf("max_tokens must be zero or positive")
	}
	if resolved.ContextWindow < 0 {
		return nil, fmt.Errorf("context_window must be zero or positive")
	}

	providerName := NormalizeProvider(resolved.Provider)
	if providerName == "" {
		providerName = InferFromBaseURL(resolved.BaseURL)
	}
	protocol := ProtocolOf(providerName)
	if protocol == "" {
		return nil, fmt.Errorf("unsupported provider %q: use openai/anthropic, a known OpenAI-compatible vendor (deepseek, moonshot, qwen, glm, groq, xai, mistral, openrouter, together, siliconflow, ollama), or provider=openai with a custom base_url", providerName)
	}
	if strings.TrimSpace(resolved.BaseURL) == "" {
		resolved.BaseURL = ProviderBaseURL(providerName)
	}
	resolved.Provider = providerName

	if strings.TrimSpace(resolved.APIKey) == "" {
		return nil, fmt.Errorf("no API key: set --api-key, llm.api_key, or CYBER_API_KEY")
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
// image content parts based on the protocol and model name heuristics.
func inferImageSupport(provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))

	if isKnownMultimodalModel(m) {
		return true
	}
	if isKnownTextOnlyModel(m) {
		return false
	}

	switch ProtocolOf(p) {
	case "anthropic":
		return true
	}

	return false
}

func NewProviderFromResolved(cfg *ProviderConfig) (Provider, error) {
	switch ProtocolOf(cfg.Provider) {
	case ProviderAnthropic:
		return NewAnthropicProvider(cfg)
	case ProviderOpenAI:
		return NewOpenAIProvider(cfg)
	default:
		return nil, fmt.Errorf("unsupported provider %q: use openai or anthropic", cfg.Provider)
	}
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
	// DeepSeek V4.1 Flash and its official aliases support vision. V4 Pro
	// still does not; keep the broader DeepSeek family text-only.
	name := model[strings.LastIndex(model, "/")+1:]
	switch name {
	case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		return true
	}
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
