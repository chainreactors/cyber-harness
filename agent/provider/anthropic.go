package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
)

const (
	defaultAnthropicMaxToken = 4096
	anthropicVersion         = "2023-06-01"
)

type AnthropicProvider struct {
	config            *ProviderConfig
	client            *http.Client
	webSearchDisabled bool
}

func NewAnthropicProvider(cfg *ProviderConfig) (*AnthropicProvider, error) {
	client, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	return &AnthropicProvider{config: cfg, client: client}, nil
}

func (p *AnthropicProvider) Name() string {
	return p.config.Provider
}

func (p *AnthropicProvider) supportsImages() bool {
	if p.config.Images != nil {
		return *p.config.Images
	}
	return true
}

func (p *AnthropicProvider) DisableImages() {
	v := false
	p.config.Images = &v
}

func (p *AnthropicProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false
	if !p.supportsImages() {
		req.Messages = StripImageParts(req.Messages)
	}

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	captureFrame(ctx, RawFrame{Provider: p.Name(), Protocol: ProviderAnthropic, Direction: "request", Transport: "http", Payload: bodyBytes, MediaType: "application/json"})

	data, err := (&apiRequest{client: p.client, timeout: timeoutFromConfig(p.config.Timeout)}).do(
		ctx, "POST", p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
	)
	if err != nil {
		captureAPIErrorFrame(ctx, p.Name(), ProviderAnthropic, err)
		return nil, hint404(err, p.completionEndpoint(), "OpenAI", "openai")
	}
	captureFrame(ctx, RawFrame{Provider: p.Name(), Protocol: ProviderAnthropic, Direction: "response", Transport: "http", Payload: data, MediaType: "application/json"})

	result, err := parseAnthropicResponse(data)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return result, nil
}

func (p *AnthropicProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = true
	if !p.supportsImages() {
		req.Messages = StripImageParts(req.Messages)
	}

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	parser := &anthropicStreamParser{}
	events, err := streamSSE(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		p.completionEndpoint(), bodyBytes, p.setAuthHeaders, p.Name(), ProviderAnthropic,
		false,
		parser.parse,
	)
	if err != nil {
		return nil, hint404(err, p.completionEndpoint(), "OpenAI", "openai")
	}
	return events, nil
}

func (p *AnthropicProvider) completionEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	return base + "/messages"
}

func (p *AnthropicProvider) setAuthHeaders(req *http.Request) {
	if p.config.APIKey != "" {
		req.Header.Set("x-api-key", p.config.APIKey)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
}

func (p *AnthropicProvider) modelsEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	base = strings.TrimSuffix(base, "/messages")
	return strings.TrimSuffix(base, "/") + "/models"
}

// ListModels enumerates the model IDs advertised by GET {base}/models. The
// Anthropic Messages API and the OpenAI-compatible gateways that front it both
// answer this route with a {"data":[{"id":...}]} list, so the settings UI can
// offer a model picklist under provider=anthropic instead of erroring with
// "provider does not support listing models". Implementing this satisfies the
// probe's modelLister interface for the Anthropic provider.
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {
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

type cacheControlMarker struct {
	Type string `json:"type"`
}

type anthropicTool struct {
	Type         string                 `json:"type,omitempty"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	CacheControl *cacheControlMarker    `json:"cache_control,omitempty"`
}

func (p *AnthropicProvider) marshalRequest(req *ChatCompletionRequest) ([]byte, error) {
	cacheEnabled := req.CacheRetention != CacheNone

	var tools []anthropicTool
	official := strings.Contains(p.config.BaseURL, "anthropic.com")
	for _, def := range req.Tools {
		inputSchema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		if def.InputSchema != nil {
			var schema map[string]interface{}
			if err := json.Unmarshal(def.InputSchema.Data, &schema); err == nil && schema != nil {
				inputSchema = schema
			}
		}
		at := anthropicTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: inputSchema,
		}
		if official {
			at.Type = "custom"
		}
		tools = append(tools, at)
	}
	if cacheEnabled && len(tools) > 0 {
		tools[len(tools)-1].CacheControl = &cacheControlMarker{Type: "ephemeral"}
	}

	var systemParts []string
	var messages []aMsg
	for _, m := range req.Messages {
		if m == nil {
			continue
		}
		switch m.Role {
		case "system":
			if text := MessageText(m); text != "" {
				systemParts = append(systemParts, text)
			}

		case "assistant":
			var blocks []map[string]interface{}
			if text := MessageText(m); text != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": text})
			}
			for _, call := range MessageToolCalls(m) {
				var input interface{}
				args := ""
				if call.Arguments != nil {
					args = strings.TrimSpace(string(call.Arguments.Data))
				}
				if args == "" {
					input = map[string]interface{}{}
				} else if err := json.Unmarshal([]byte(args), &input); err != nil {
					return nil, fmt.Errorf("anthropic tool call %q has invalid JSON arguments: %w", call.Name, err)
				}
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    call.Id,
					"name":  call.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": ""})
			}
			messages = append(messages, aMsg{Role: "assistant", Content: blocks})

		case "tool":
			result := MessageToolResult(m)
			if result == nil {
				continue
			}
			resultBlock := map[string]interface{}{
				"type":        "tool_result",
				"tool_use_id": result.CallId,
				"content":     toolResultToAnthropicContent(result),
			}
			if result.IsError {
				resultBlock["is_error"] = true
			}
			messages = append(messages, aMsg{
				Role:    "user",
				Content: []map[string]interface{}{resultBlock},
			})

		default:
			blocks := messageContentToAnthropicBlocks(m)
			messages = append(messages, aMsg{Role: m.Role, Content: blocks})
		}
	}

	merged := mergeConsecutive(messages)

	if cacheEnabled {
		for i := len(merged) - 1; i >= 0; i-- {
			if merged[i].Role == "user" && len(merged[i].Content) > 0 {
				last := merged[i].Content[len(merged[i].Content)-1]
				last["cache_control"] = map[string]interface{}{"type": "ephemeral"}
				break
			}
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxToken
	}

	systemText := strings.Join(systemParts, "\n\n")
	var system interface{}
	if cacheEnabled && systemText != "" {
		system = []map[string]interface{}{{
			"type":          "text",
			"text":          systemText,
			"cache_control": map[string]interface{}{"type": "ephemeral"},
		}}
	} else {
		system = systemText
	}

	wrapper := struct {
		Model       string          `json:"model"`
		Messages    []aMsg          `json:"messages"`
		System      interface{}     `json:"system,omitempty"`
		Tools       []anthropicTool `json:"tools,omitempty"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Temperature *float64        `json:"temperature,omitempty"`
		Stream      bool            `json:"stream,omitempty"`
	}{
		Model:       req.Model,
		Messages:    merged,
		System:      system,
		Tools:       tools,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      req.Stream,
	}
	return json.Marshal(wrapper)
}

func toolResultToAnthropicContent(result *aop.ToolResult) interface{} {
	blocks := messageBlocksFromContents(result.Output)
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 && blocks[0]["type"] == "text" {
		return blocks[0]["text"]
	}
	return blocks
}

func messageContentToAnthropicBlocks(m *aop.Message) []map[string]interface{} {
	blocks := messageBlocksFromContents(m.Content)
	if len(blocks) == 0 {
		return []map[string]interface{}{{"type": "text", "text": ""}}
	}
	return blocks
}

func messageBlocksFromContents(contents []*aop.Content) []map[string]interface{} {
	var blocks []map[string]interface{}
	for _, content := range contents {
		switch value := content.Value.(type) {
		case *aop.Content_Text:
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": value.Text.Text})
		case *aop.Content_Media:
			media := value.Media
			if media.Kind != "image" || media.Resource == nil {
				continue
			}
			data := media.Resource.GetData()
			if len(data) == 0 {
				if uri := media.Resource.GetUri(); uri != "" {
					blocks = append(blocks, map[string]interface{}{
						"type":   "image",
						"source": map[string]interface{}{"type": "url", "url": uri},
					})
				}
				continue
			}
			blocks = append(blocks, map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": media.Resource.MediaType,
					"data":       base64.StdEncoding.EncodeToString(data),
				},
			})
		}
	}
	return blocks
}

// --- Anthropic response types and parsing ---

type aMsg struct {
	Role    string                   `json:"role"`
	Content []map[string]interface{} `json:"content"`
}

func mergeConsecutive(msgs []aMsg) []aMsg {
	if len(msgs) == 0 {
		return msgs
	}
	merged := []aMsg{msgs[0]}
	for _, m := range msgs[1:] {
		last := &merged[len(merged)-1]
		if last.Role == m.Role {
			last.Content = append(last.Content, m.Content...)
		} else {
			merged = append(merged, m)
		}
	}
	return merged
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type anthropicContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthropicMessageResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      *anthropicUsage         `json:"usage,omitempty"`
	Error      *APIError               `json:"error,omitempty"`
}

func parseAnthropicResponse(data []byte) (*ChatCompletionResponse, error) {
	var probe struct {
		Type  string    `json:"type"`
		Error *APIError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.Type == "error" && probe.Error != nil {
		return nil, probe.Error
	}

	var resp anthropicMessageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	msg := anthropicBlocksToMessage(resp.Role, resp.Content)
	return &ChatCompletionResponse{
		ID: resp.ID,
		Choices: []Choice{{
			Message:      msg,
			FinishReason: mapAnthropicStopReason(resp.StopReason),
		}},
		Usage: convertAnthropicUsage(resp.Usage),
	}, nil
}

func anthropicBlocksToMessage(role string, blocks []anthropicContentBlock) *aop.Message {
	if role == "" {
		role = "assistant"
	}
	msg := &aop.Message{Role: role}
	var text, thinking strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := anthropicToolArguments(block.Input)
			msg.Content = append(msg.Content, &aop.Content{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
				Id:        block.ID,
				Name:      block.Name,
				Kind:      "function",
				Arguments: &aop.EncodedValue{Data: []byte(args), MediaType: aop.JSONMediaType},
			}}})
		}
	}
	if content := thinking.String(); content != "" {
		msg.Content = append([]*aop.Content{aop.Reasoning(content)}, msg.Content...)
	}
	if content := text.String(); content != "" {
		insertAt := 0
		if len(msg.Content) > 0 && msg.Content[0].GetReasoning() != nil {
			insertAt = 1
		}
		msg.Content = append(msg.Content[:insertAt], append([]*aop.Content{aop.Text(content)}, msg.Content[insertAt:]...)...)
	}
	return msg
}

func anthropicToolArguments(input json.RawMessage) string {
	args := strings.TrimSpace(string(input))
	if args == "" || args == "null" {
		return "{}"
	}
	return args
}

func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func convertAnthropicUsage(usage *anthropicUsage) *aop.TokenUsage {
	if usage == nil {
		return nil
	}
	promptTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	completionTokens := usage.OutputTokens
	return TokenUsage(promptTokens, completionTokens, promptTokens+completionTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens)
}

// --- Anthropic streaming ---

type anthropicStreamParser struct {
	usage anthropicUsage
}

func (p *anthropicStreamParser) parse(eventName string, data []byte) (ChatCompletionStreamEvent, error) {
	var probe struct {
		Type  string    `json:"type"`
		Error *APIError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal anthropic stream event: %w", err)
	}
	eventType := probe.Type
	if eventType == "" {
		eventType = eventName
	}
	if probe.Error != nil {
		return ChatCompletionStreamEvent{}, probe.Error
	}

	switch eventType {
	case "message_start":
		var event struct {
			Message struct {
				Role  string          `json:"role"`
				Usage *anthropicUsage `json:"usage,omitempty"`
			} `json:"message"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal anthropic message_start: %w", err)
		}
		p.mergeUsage(event.Message.Usage)
		role := event.Message.Role
		if role == "" {
			role = "assistant"
		}
		return ChatCompletionStreamEvent{
			Role:  role,
			Usage: p.usageSnapshot(),
		}, nil

	case "content_block_start":
		var event struct {
			Index        int                   `json:"index"`
			ContentBlock anthropicContentBlock `json:"content_block"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal anthropic content_block_start: %w", err)
		}
		switch event.ContentBlock.Type {
		case "text":
			if event.ContentBlock.Text == "" {
				return ChatCompletionStreamEvent{}, nil
			}
			return ChatCompletionStreamEvent{MessageDelta: &aop.MessageDelta{
				Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
				Value:     &aop.MessageDelta_Text{Text: event.ContentBlock.Text},
			}}, nil
		case "tool_use":
			delta := &aop.ToolCallDelta{
				Index:  uint32(event.Index),
				CallId: event.ContentBlock.ID,
				Name:   event.ContentBlock.Name,
			}
			if args := anthropicToolArguments(event.ContentBlock.Input); args != "{}" {
				delta.Arguments = []byte(args)
			}
			return ChatCompletionStreamEvent{ToolDeltas: []*aop.ToolCallDelta{delta}}, nil
		default:
			return ChatCompletionStreamEvent{}, nil
		}

	case "content_block_delta":
		var event struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text,omitempty"`
				PartialJSON string `json:"partial_json,omitempty"`
				Thinking    string `json:"thinking,omitempty"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal anthropic content_block_delta: %w", err)
		}
		switch event.Delta.Type {
		case "text_delta":
			return ChatCompletionStreamEvent{MessageDelta: &aop.MessageDelta{
				Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
				Value:     &aop.MessageDelta_Text{Text: event.Delta.Text},
			}}, nil
		case "input_json_delta":
			return ChatCompletionStreamEvent{
				ToolDeltas: []*aop.ToolCallDelta{{
					Index:     uint32(event.Index),
					Arguments: []byte(event.Delta.PartialJSON),
				}},
			}, nil
		case "thinking_delta":
			return ChatCompletionStreamEvent{MessageDelta: &aop.MessageDelta{
				Operation: aop.DeltaOperation_DELTA_OPERATION_APPEND,
				Value:     &aop.MessageDelta_Reasoning{Reasoning: event.Delta.Thinking},
			}}, nil
		default:
			return ChatCompletionStreamEvent{}, nil
		}

	case "message_delta":
		var event struct {
			Delta struct {
				StopReason string `json:"stop_reason,omitempty"`
			} `json:"delta"`
			Usage *anthropicUsage `json:"usage,omitempty"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return ChatCompletionStreamEvent{}, fmt.Errorf("unmarshal anthropic message_delta: %w", err)
		}
		p.mergeUsage(event.Usage)
		return ChatCompletionStreamEvent{
			FinishReason: mapAnthropicStopReason(event.Delta.StopReason),
			Usage:        p.usageSnapshot(),
		}, nil

	case "message_stop":
		return ChatCompletionStreamEvent{Done: true, Usage: p.usageSnapshot()}, nil

	case "content_block_stop", "ping":
		return ChatCompletionStreamEvent{}, nil

	case "error":
		if probe.Error == nil {
			return ChatCompletionStreamEvent{}, fmt.Errorf("API error: anthropic stream error event without details")
		}
		return ChatCompletionStreamEvent{}, probe.Error

	default:
		return ChatCompletionStreamEvent{}, nil
	}
}

func (p *anthropicStreamParser) mergeUsage(usage *anthropicUsage) {
	if usage == nil {
		return
	}
	if usage.InputTokens > 0 {
		p.usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		p.usage.OutputTokens = usage.OutputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		p.usage.CacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	if usage.CacheReadInputTokens > 0 {
		p.usage.CacheReadInputTokens = usage.CacheReadInputTokens
	}
}

func (p *anthropicStreamParser) usageSnapshot() *aop.TokenUsage {
	if p.usage.InputTokens == 0 &&
		p.usage.OutputTokens == 0 &&
		p.usage.CacheCreationInputTokens == 0 &&
		p.usage.CacheReadInputTokens == 0 {
		return nil
	}
	return convertAnthropicUsage(&p.usage)
}

// --- WebSearch via Anthropic server-side web_search tool ---

func (p *AnthropicProvider) WebSearch(ctx context.Context, query string, maxResults int) (*WebSearchResponse, error) {
	if p.webSearchDisabled {
		return nil, fmt.Errorf("provider does not support server-side web search")
	}
	maxResults = clampInt(maxResults, 1, 10, 5)

	data, err := doJSON(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		http.MethodPost, p.completionEndpoint(),
		map[string]any{
			"model":      p.config.Model,
			"max_tokens": defaultAnthropicMaxToken,
			"tools": []map[string]any{{
				"type": "web_search_20250305", "name": "web_search", "max_uses": maxResults,
			}},
			"messages": []map[string]string{{"role": "user", "content": "Search the web for: " + query}},
		},
		func(req *http.Request) {
			p.setAuthHeaders(req)
			req.Header.Set("anthropic-beta", "web-search-2025-03-05")
		},
	)
	if err != nil {
		p.webSearchDisabled = true
		return nil, err
	}
	resp, err := parseAnthropicWebSearchResponse(data)
	if err != nil {
		p.webSearchDisabled = true
		return nil, err
	}
	return resp, nil
}

func parseAnthropicWebSearchResponse(data []byte) (*WebSearchResponse, error) {
	var probe struct {
		Type  string    `json:"type"`
		Error *APIError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.Type == "error" && probe.Error != nil {
		return nil, probe.Error
	}

	var raw struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text,omitempty"`
			Content json.RawMessage `json:"content,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse web search response: %w", err)
	}

	out := &WebSearchResponse{}
	for _, block := range raw.Content {
		switch block.Type {
		case "web_search_tool_result":
			var results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			}
			if json.Unmarshal(block.Content, &results) == nil {
				for _, r := range results {
					if r.URL == "" {
						continue
					}
					out.Results = append(out.Results, WebSearchResult{Title: r.Title, URL: r.URL})
				}
			}
		case "text":
			if t := strings.TrimSpace(block.Text); t != "" {
				out.Summary += t + "\n"
			}
		}
	}
	out.Summary = strings.TrimSpace(out.Summary)
	return out, nil
}
