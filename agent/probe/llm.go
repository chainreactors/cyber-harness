package probe

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
)

// LLMProbeRequest carries the connection parameters the user wants to verify
// or use for model enumeration. It mirrors the LLM section of
// webproto.DistributeConfig. An empty APIKey means "use the key already stored
// in the config" (matching the settings UI where a configured key is left blank
// to keep it unchanged). Model is only required for TestLLM; ListLLMModels
// ignores it.
type LLMProbeRequest struct {
	ProfileID string `json:"profile_id,omitempty"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model,omitempty"`
	Proxy     string `json:"proxy"`
}

// LLMTestResult reports whether a probe request reached the provider and
// returned a usable completion.
type LLMTestResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	LatencyMs int64  `json:"latency_ms"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// llmProbeTimeout bounds a single connectivity test so a misconfigured or
// unreachable endpoint fails fast instead of hanging the settings dialog.
const llmProbeTimeout = 30 * time.Second

// LLMModelsResult reports the model IDs discovered at the endpoint. ok=false
// carries the reason (unsupported provider, auth failure, unreachable, …).
type LLMModelsResult struct {
	OK        bool     `json:"ok"`
	Supported bool     `json:"supported"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// modelLister is the optional capability a provider implements when its
// endpoint exposes a model catalog (the OpenAI-compatible GET /models route).
type modelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// ListLLMModels asks the configured endpoint for its model catalog so the
// settings UI can offer a picklist instead of requiring the model to be typed
// by hand. Like TestLLM it never returns a transport error — failures are
// captured inside LLMModelsResult. When req.APIKey is blank, storedAPIKey is
// used (the settings UI leaves a configured key blank to keep it unchanged).
func ListLLMModels(ctx context.Context, req LLMProbeRequest, storedAPIKey string) (LLMModelsResult, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(storedAPIKey)
	}

	cfg := agent.ProviderConfig{
		Provider: strings.TrimSpace(req.Provider),
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   apiKey,
		Proxy:    strings.TrimSpace(req.Proxy),
		Timeout:  int(llmProbeTimeout / time.Second),
	}

	var result LLMModelsResult

	prov, err := agent.NewProvider(&cfg)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	lister, ok := prov.(modelLister)
	if !ok {
		result.Error = "provider does not support listing models"
		return result, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, llmProbeTimeout)
	defer cancel()

	models, err := lister.ListModels(probeCtx)
	if err != nil {
		var apiErr *agent.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			result.OK = true
			return result, nil
		}
		result.Error = err.Error()
		return result, nil
	}

	result.OK = true
	result.Supported = true
	result.Models = models
	return result, nil
}

// TestLLM issues a minimal chat completion against the supplied LLM settings
// and reports the outcome. It never returns a transport error to the caller —
// failures are captured inside LLMTestResult so the UI can render them. A nil
// error only signals the request was well-formed enough to attempt. When
// req.APIKey is blank, storedAPIKey is used (the settings UI leaves a configured
// key blank to keep it unchanged).
func TestLLM(ctx context.Context, req LLMProbeRequest, storedAPIKey string) (LLMTestResult, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(storedAPIKey)
	}

	cfg := agent.ProviderConfig{
		Provider: strings.TrimSpace(req.Provider),
		BaseURL:  strings.TrimSpace(req.BaseURL),
		APIKey:   apiKey,
		Model:    strings.TrimSpace(req.Model),
		Proxy:    strings.TrimSpace(req.Proxy),
		Timeout:  int(llmProbeTimeout / time.Second),
	}

	result := LLMTestResult{Provider: cfg.Provider, Model: cfg.Model}

	if cfg.Model == "" {
		result.Error = "model is required"
		return result, nil
	}

	prov, err := agent.NewProvider(&cfg)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, llmProbeTimeout)
	defer cancel()

	maxTokens := 16
	start := time.Now()
	resp, err := prov.ChatCompletion(probeCtx, &agent.ChatCompletionRequest{
		Model:     cfg.Model,
		Messages:  []agent.ChatMessage{agent.NewTextMessage("user", "ping")},
		MaxTokens: maxTokens,
	})
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	if len(resp.Choices) == 0 {
		result.Error = "provider returned no choices"
		return result, nil
	}

	result.OK = true
	if msg := resp.Choices[0].Message; msg.Content != nil {
		result.Reply = strings.TrimSpace(*msg.Content)
	}
	return result, nil
}
