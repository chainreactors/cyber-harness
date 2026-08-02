package probe

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
)

// A blank API key means "use the key already stored"
// in the config" (matching the settings UI where a configured key is left blank
// to keep it unchanged). Model is only required for TestLLM; ListLLMModels
// ignores it.
//
// llmProbeTimeout bounds a single connectivity test so a misconfigured or
// unreachable endpoint fails fast instead of hanging the settings dialog.
const llmProbeTimeout = 30 * time.Second

// modelLister is the optional capability a provider implements when its
// endpoint exposes a model catalog (the OpenAI-compatible GET /models route).
type modelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// ListLLMModels asks the configured endpoint for its model catalog so the
// settings UI can offer a picklist instead of requiring the model to be typed
// by hand. Like TestLLM it never returns a transport error — failures are
// captured inside ListModelsResult. When req.ApiKey is blank, storedAPIKey is
// used (the settings UI leaves a configured key blank to keep it unchanged).
func ListLLMModels(ctx context.Context, req *types.LLMProbeRequest, storedAPIKey string) (*types.ListModelsResult, error) {
	apiKey := strings.TrimSpace(req.GetApiKey())
	if apiKey == "" {
		apiKey = strings.TrimSpace(storedAPIKey)
	}

	cfg := agent.ProviderConfig{
		Provider: strings.TrimSpace(req.GetProvider()),
		BaseURL:  strings.TrimSpace(req.GetBaseUrl()),
		APIKey:   apiKey,
		Proxy:    strings.TrimSpace(req.GetProxy()),
		Timeout:  int(llmProbeTimeout / time.Second),
	}

	result := new(types.ListModelsResult)

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
			result.Ok = true
			return result, nil
		}
		result.Error = err.Error()
		return result, nil
	}

	result.Ok = true
	result.Supported = true
	result.Models = models
	return result, nil
}

// TestLLM issues a minimal chat completion against the supplied LLM settings
// and reports the outcome. It never returns a transport error to the caller —
// failures are captured inside LLMProbeResult so the UI can render them. A nil
// error only signals the request was well-formed enough to attempt. When
// req.ApiKey is blank, storedAPIKey is used (the settings UI leaves a configured
// key blank to keep it unchanged).
func TestLLM(ctx context.Context, req *types.LLMProbeRequest, storedAPIKey string) (*types.LLMProbeResult, error) {
	apiKey := strings.TrimSpace(req.GetApiKey())
	if apiKey == "" {
		apiKey = strings.TrimSpace(storedAPIKey)
	}

	cfg := agent.ProviderConfig{
		Provider: strings.TrimSpace(req.GetProvider()),
		BaseURL:  strings.TrimSpace(req.GetBaseUrl()),
		APIKey:   apiKey,
		Model:    strings.TrimSpace(req.GetModel()),
		Proxy:    strings.TrimSpace(req.GetProxy()),
		Timeout:  int(llmProbeTimeout / time.Second),
	}

	result := &types.LLMProbeResult{Provider: cfg.Provider, Model: cfg.Model}

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
		Messages:  []*aop.Message{provider.TextMessage("user", "ping")},
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

	result.Ok = true
	result.Reply = strings.TrimSpace(provider.MessageText(resp.Choices[0].Message))
	return result, nil
}
