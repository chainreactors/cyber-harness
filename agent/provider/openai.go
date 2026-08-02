package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
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

	return parseOpenAIResponse(data)
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

// --- OpenAI wire format ---

type openAIMessage struct {
	Name             string           `json:"name,omitempty"`
	Role             string           `json:"role"`
	Content          any              `json:"content"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

type openAIContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL    string `json:"url"`
		Detail string `json:"detail,omitempty"`
	} `json:"image_url,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// aopToOpenAIMessages flattens aop messages into the OpenAI chat format. A
// tool-role aop message (carrying a ToolResult) maps to a tool message; media
// and text parts map to content arrays.
func aopToOpenAIMessages(messages []*aop.Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		// Some OpenAI-compatible gateways model `content` as a required field for
		// every message, including assistant tool calls and empty tool results.
		// Start with an explicit empty string and replace it with multipart content
		// below when media is present.
		wire := openAIMessage{Role: m.Role, Name: m.Name, Content: ""}
		var text strings.Builder
		var parts []openAIContentPart
		for _, content := range m.Content {
			switch value := content.Value.(type) {
			case *aop.Content_Text:
				if len(parts) > 0 {
					parts = append(parts, openAIContentPart{Type: "text", Text: value.Text.Text})
				} else {
					text.WriteString(value.Text.Text)
				}
			case *aop.Content_Reasoning:
				wire.ReasoningContent = value.Reasoning.Text
			case *aop.Content_Media:
				if media := value.Media; media.Kind == "image" && media.Resource != nil {
					if data := media.Resource.GetData(); len(data) > 0 {
						url := "data:" + media.Resource.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
						parts = append(parts, openAIContentPart{Type: "image_url", ImageURL: &struct {
							URL    string `json:"url"`
							Detail string `json:"detail,omitempty"`
						}{URL: url, Detail: "high"}})
					}
				}
			case *aop.Content_ToolCall:
				call := value.ToolCall
				args := ""
				if call.Arguments != nil {
					args = string(call.Arguments.Data)
				}
				var tc openAIToolCall
				tc.ID = call.Id
				tc.Type = "function"
				tc.Function.Name = call.Name
				tc.Function.Arguments = args
				wire.ToolCalls = append(wire.ToolCalls, tc)
			case *aop.Content_ToolResult:
				result := value.ToolResult
				wire.Role = "tool"
				wire.ToolCallID = result.CallId
				for _, block := range result.Output {
					if t := block.GetText(); t != nil {
						text.WriteString(t.Text)
					}
					if media := block.GetMedia(); media != nil && media.Kind == "image" && media.Resource != nil {
						if data := media.Resource.GetData(); len(data) > 0 {
							url := "data:" + media.Resource.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
							parts = append(parts, openAIContentPart{Type: "image_url", ImageURL: &struct {
								URL    string `json:"url"`
								Detail string `json:"detail,omitempty"`
							}{URL: url, Detail: "high"}})
						}
					}
				}
			}
		}
		if len(parts) > 0 {
			all := make([]openAIContentPart, 0, len(parts)+1)
			if text.Len() > 0 {
				all = append(all, openAIContentPart{Type: "text", Text: text.String()})
			}
			wire.Content = append(all, parts...)
		} else if wire.ToolCallID == "" || text.Len() > 0 {
			wire.Content = text.String()
		}
		out = append(out, wire)
	}
	return out
}

func marshalOpenAIRequest(req *ChatCompletionRequest) ([]byte, error) {
	messages := aopToOpenAIMessages(req.Messages)
	var tools []openAITool
	for _, def := range req.Tools {
		var t openAITool
		t.Type = "function"
		t.Function.Name = def.Name
		t.Function.Description = def.Description
		if def.InputSchema != nil {
			var schema map[string]any
			if err := json.Unmarshal(def.InputSchema.Data, &schema); err == nil {
				t.Function.Parameters = schema
			}
		}
		if t.Function.Parameters == nil {
			t.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, t)
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.Stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if req.CacheRetention != CacheNone && req.SessionID != "" {
		body["prompt_cache_key"] = req.SessionID
		if req.CacheRetention == CacheLong {
			body["prompt_cache_retention"] = "24h"
		}
	}
	return json.Marshal(body)
}

// --- OpenAI response parsing ---

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

func (u *openAIUsage) UnmarshalJSON(data []byte) error {
	type plain openAIUsage
	var raw struct {
		plain
		// OpenAI format
		PromptTokensDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details,omitempty"`
		// DeepSeek format
		PromptCacheHitTokens  *int `json:"prompt_cache_hit_tokens,omitempty"`
		PromptCacheMissTokens *int `json:"prompt_cache_miss_tokens,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = openAIUsage(raw.plain)
	if raw.PromptTokensDetails != nil {
		u.CacheReadTokens = raw.PromptTokensDetails.CachedTokens
		u.CacheWriteTokens = raw.PromptTokensDetails.CacheWriteTokens
	} else if raw.PromptCacheHitTokens != nil {
		u.CacheReadTokens = *raw.PromptCacheHitTokens
		if raw.PromptCacheMissTokens != nil {
			u.CacheWriteTokens = *raw.PromptCacheMissTokens
		}
	}
	return nil
}

func (u *openAIUsage) toProto() *aop.TokenUsage {
	if u == nil {
		return nil
	}
	return TokenUsage(u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CacheReadTokens, u.CacheWriteTokens)
}

type openAIResponseMessage struct {
	Role             string           `json:"role"`
	Content          *string          `json:"content"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
}

func openAIMessageToAOP(msg *openAIResponseMessage) *aop.Message {
	if msg.Role == "" {
		msg.Role = "assistant"
	}
	out := &aop.Message{Role: msg.Role}
	if msg.ReasoningContent != nil && *msg.ReasoningContent != "" {
		out.Content = append(out.Content, aop.Reasoning(*msg.ReasoningContent))
	}
	if msg.Content != nil && *msg.Content != "" {
		out.Content = append(out.Content, aop.Text(*msg.Content))
	}
	for _, tc := range msg.ToolCalls {
		var arguments *aop.EncodedValue
		if tc.Function.Arguments != "" {
			arguments = &aop.EncodedValue{Data: []byte(tc.Function.Arguments), MediaType: aop.JSONMediaType}
		}
		kind := tc.Type
		if kind == "" {
			kind = "function"
		}
		out.Content = append(out.Content, &aop.Content{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
			Id: tc.ID, Name: tc.Function.Name, Kind: kind, Arguments: arguments,
		}}})
	}
	return out
}

func parseOpenAIResponse(data []byte) (*ChatCompletionResponse, error) {
	var raw struct {
		ID      string `json:"id"`
		Choices []struct {
			Message      openAIResponseMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		} `json:"choices"`
		Usage *openAIUsage `json:"usage,omitempty"`
		Error *APIError    `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if raw.Error != nil {
		return nil, raw.Error
	}
	result := &ChatCompletionResponse{ID: raw.ID, Usage: raw.Usage.toProto()}
	for _, choice := range raw.Choices {
		msg := choice.Message
		result.Choices = append(result.Choices, Choice{
			Message:      openAIMessageToAOP(&msg),
			FinishReason: choice.FinishReason,
		})
	}
	return result, nil
}

// --- OpenAI streaming ---

type openAIStreamDelta struct {
	Role             string  `json:"role,omitempty"`
	Content          *string `json:"content"`
	ReasoningContent *string `json:"reasoning_content,omitempty"`
	ToolCalls        []struct {
		Index    int    `json:"index,omitempty"`
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Function struct {
			Name      string `json:"name,omitempty"`
			Arguments string `json:"arguments,omitempty"`
		} `json:"function,omitempty"`
	} `json:"tool_calls,omitempty"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta        openAIStreamDelta `json:"delta"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage,omitempty"`
	Error *APIError    `json:"error,omitempty"`
}

func parseOpenAIStreamChunk(data []byte) (ChatCompletionStreamEvent, error) {
	var chunk openAIStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal stream chunk: %w", err)
	}
	if chunk.Error != nil {
		return ChatCompletionStreamEvent{}, chunk.Error
	}
	event := ChatCompletionStreamEvent{Usage: chunk.Usage.toProto()}
	if len(chunk.Choices) == 0 {
		return event, nil
	}
	delta := chunk.Choices[0].Delta
	event.Role = delta.Role
	event.FinishReason = chunk.Choices[0].FinishReason
	if delta.Content != nil && *delta.Content != "" {
		event.MessageDelta = &aop.MessageDelta{
			Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
			Value:     &aop.MessageDelta_Text{Text: *delta.Content},
		}
	} else if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		event.MessageDelta = &aop.MessageDelta{
			Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
			Value:     &aop.MessageDelta_Reasoning{Reasoning: *delta.ReasoningContent},
		}
	}
	for _, tc := range delta.ToolCalls {
		callDelta := &aop.ToolCallDelta{
			Index: uint32(tc.Index), CallId: tc.ID, Name: tc.Function.Name,
		}
		if tc.Function.Arguments != "" {
			callDelta.Arguments = []byte(tc.Function.Arguments)
		}
		event.ToolDeltas = append(event.ToolDeltas, callDelta)
	}
	return event, nil
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
