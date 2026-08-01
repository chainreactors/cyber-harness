package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type OpenAIProvider struct {
	config            *ProviderConfig
	client            *http.Client
	webSearchDisabled bool
}

func NewOpenAIProvider(cfg *ProviderConfig) (*OpenAIProvider, error) {
	client, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	return &OpenAIProvider{config: cfg, client: client}, nil
}

func (p *OpenAIProvider) Name() string {
	return p.config.Provider
}

func (p *OpenAIProvider) supportsImages() bool {
	if p.config.Images != nil {
		return *p.config.Images
	}
	return false
}

func (p *OpenAIProvider) DisableImages() {
	v := false
	p.config.Images = &v
}

func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false
	if !p.supportsImages() {
		req.Messages = StripImageParts(req.Messages)
	}

	bodyBytes, err := marshalOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	captureFrame(ctx, RawFrame{Provider: p.Name(), Protocol: ProviderOpenAI, Direction: "request", Transport: "http", Payload: bodyBytes, MediaType: "application/json"})

	data, err := (&apiRequest{client: p.client, timeout: timeoutFromConfig(p.config.Timeout)}).do(
		ctx, "POST", p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
	)
	if err != nil {
		captureAPIErrorFrame(ctx, p.Name(), ProviderOpenAI, err)
		return nil, hint404(err, p.completionEndpoint(), "Anthropic", "anthropic")
	}
	captureFrame(ctx, RawFrame{Provider: p.Name(), Protocol: ProviderOpenAI, Direction: "response", Transport: "http", Payload: data, MediaType: "application/json"})

	var result ChatCompletionResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &result, nil
}

func (p *OpenAIProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = true
	if !p.supportsImages() {
		req.Messages = StripImageParts(req.Messages)
	}

	bodyBytes, err := marshalOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	events, err := streamSSE(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		p.completionEndpoint(), bodyBytes, p.setAuthHeaders, p.Name(), ProviderOpenAI,
		true,
		func(_ string, data []byte) (ChatCompletionStreamEvent, error) {
			return parseOpenAIStreamChunk(data)
		},
	)
	if err != nil {
		return nil, hint404(err, p.completionEndpoint(), "Anthropic", "anthropic")
	}
	return events, nil
}

func (p *OpenAIProvider) completionEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	return base + "/chat/completions"
}

func (p *OpenAIProvider) modelsEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	return base + "/models"
}

// ListModels enumerates the model IDs the endpoint advertises via the
// OpenAI-compatible GET /models route. Most third-party gateways implement it,
// so the settings UI can offer a picklist instead of a free-text field.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]string, error) {
	data, err := (&apiRequest{client: p.client, timeout: timeoutFromConfig(p.config.Timeout)}).do(
		ctx, "GET", p.modelsEndpoint(), nil, p.setAuthHeaders,
	)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal models: %w", err)
	}
	ids := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (p *OpenAIProvider) setAuthHeaders(req *http.Request) {
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
}

func marshalOpenAIRequest(req *ChatCompletionRequest) ([]byte, error) {
	type streamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	type wrapper struct {
		*ChatCompletionRequest
		StreamOptions        *streamOptions `json:"stream_options,omitempty"`
		PromptCacheKey       string         `json:"prompt_cache_key,omitempty"`
		PromptCacheRetention string         `json:"prompt_cache_retention,omitempty"`
	}
	w := wrapper{ChatCompletionRequest: req}
	if req.Stream {
		w.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.CacheRetention != CacheNone && req.SessionID != "" {
		w.PromptCacheKey = req.SessionID
		if req.CacheRetention == CacheLong {
			w.PromptCacheRetention = "24h"
		}
	}
	return json.Marshal(w)
}

// --- WebSearch via OpenAI Responses API ---

func (p *OpenAIProvider) WebSearch(ctx context.Context, query string, maxResults int) (*WebSearchResponse, error) {
	if p.webSearchDisabled {
		return nil, fmt.Errorf("provider does not support server-side web search")
	}
	maxResults = clampInt(maxResults, 1, 10, 5)

	base := strings.TrimSuffix(p.config.BaseURL, "/")
	endpoint := base + "/responses"

	data, err := doJSON(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		http.MethodPost, endpoint,
		map[string]any{
			"model": p.config.Model,
			"input": "Search the web for: " + query,
			"tools": []map[string]any{{"type": "web_search", "search_context_size": "medium"}},
		},
		p.setAuthHeaders,
	)
	if err != nil {
		p.webSearchDisabled = true
		return nil, err
	}
	resp, err := parseOpenAIWebSearchResponse(data, maxResults)
	if err != nil {
		p.webSearchDisabled = true
		return nil, err
	}
	return resp, nil
}

func parseOpenAIWebSearchResponse(data []byte, maxResults int) (*WebSearchResponse, error) {
	var probe struct {
		Error *APIError `json:"error,omitempty"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Error != nil {
		return nil, probe.Error
	}

	var raw struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type  string `json:"type"`
					Title string `json:"title"`
					URL   string `json:"url"`
				} `json:"annotations,omitempty"`
			} `json:"content,omitempty"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse web search response: %w", err)
	}

	out := &WebSearchResponse{}
	seen := make(map[string]struct{})
	for _, block := range raw.Output {
		if block.Type != "message" {
			continue
		}
		for _, c := range block.Content {
			if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
				out.Summary += c.Text + "\n"
			}
			for _, ann := range c.Annotations {
				if ann.Type != "url_citation" || ann.URL == "" {
					continue
				}
				if _, ok := seen[ann.URL]; ok {
					continue
				}
				seen[ann.URL] = struct{}{}
				title := ann.Title
				if title == "" {
					title = ann.URL
				}
				out.Results = append(out.Results, WebSearchResult{Title: title, URL: ann.URL})
				if len(out.Results) >= maxResults {
					break
				}
			}
		}
	}
	out.Summary = strings.TrimSpace(out.Summary)
	return out, nil
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta        ChatMessageDelta `json:"delta"`
		FinishReason string           `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage    `json:"usage,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

func parseOpenAIStreamChunk(data []byte) (ChatCompletionStreamEvent, error) {
	var chunk openAIStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal stream chunk: %w", err)
	}
	if chunk.Error != nil {
		return ChatCompletionStreamEvent{}, chunk.Error
	}
	event := ChatCompletionStreamEvent{Usage: chunk.Usage}
	if len(chunk.Choices) == 0 {
		return event, nil
	}
	event.Delta = chunk.Choices[0].Delta
	event.FinishReason = chunk.Choices[0].FinishReason
	return event, nil
}
