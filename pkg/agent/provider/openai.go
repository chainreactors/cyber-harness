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

func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false
	if !p.supportsImages() {
		req.Messages = stripImageParts(req.Messages)
	}

	bodyBytes, err := marshalOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	data, err := (&apiRequest{client: p.client, timeout: timeoutFromConfig(p.config.Timeout)}).do(
		ctx, "POST", p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
	)
	if err != nil {
		return nil, err
	}

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
		req.Messages = stripImageParts(req.Messages)
	}

	bodyBytes, err := marshalOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	return streamSSE(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
		func(_ string, data []byte) (ChatCompletionStreamEvent, error) {
			return parseOpenAIStreamChunk(data)
		},
	)
}

func (p *OpenAIProvider) completionEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	return base + "/chat/completions"
}

func (p *OpenAIProvider) setAuthHeaders(req *http.Request) {
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
}

func marshalOpenAIRequest(req *ChatCompletionRequest) ([]byte, error) {
	if req.CacheRetention == CacheNone || req.SessionID == "" {
		return json.Marshal(req)
	}
	type wrapper struct {
		*ChatCompletionRequest
		PromptCacheKey       string `json:"prompt_cache_key,omitempty"`
		PromptCacheRetention string `json:"prompt_cache_retention,omitempty"`
	}
	w := wrapper{ChatCompletionRequest: req, PromptCacheKey: req.SessionID}
	if req.CacheRetention == CacheLong {
		w.PromptCacheRetention = "24h"
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
