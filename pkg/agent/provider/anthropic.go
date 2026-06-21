package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

func (p *AnthropicProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false
	if !p.supportsImages() {
		req.Messages = stripImageParts(req.Messages)
	}

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	data, err := (&apiRequest{client: p.client, timeout: timeoutFromConfig(p.config.Timeout)}).do(
		ctx, "POST", p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
	)
	if err != nil {
		return nil, err
	}

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
		req.Messages = stripImageParts(req.Messages)
	}

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	parser := &anthropicStreamParser{}
	return streamSSE(ctx, p.client, timeoutFromConfig(p.config.Timeout),
		p.completionEndpoint(), bodyBytes, p.setAuthHeaders,
		parser.parse,
	)
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
	for _, t := range req.Tools {
		inputSchema := t.Function.Parameters
		if inputSchema == nil {
			inputSchema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		at := anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
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
		switch m.Role {
		case "system":
			if m.Content != nil {
				systemParts = append(systemParts, *m.Content)
			}

		case "assistant":
			var blocks []map[string]interface{}
			if m.Content != nil && *m.Content != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": *m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				args := strings.TrimSpace(tc.Function.Arguments)
				if args == "" {
					input = map[string]interface{}{}
				} else if err := json.Unmarshal([]byte(args), &input); err != nil {
					return nil, fmt.Errorf("anthropic tool call %q has invalid JSON arguments: %w", tc.Function.Name, err)
				}
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": ""})
			}
			messages = append(messages, aMsg{Role: "assistant", Content: blocks})

		case "tool":
			var resultContent interface{}
			if len(m.ContentParts) > 0 {
				resultContent = contentPartsToAnthropicBlocks(m.ContentParts)
			} else {
				resultContent = deref(m.Content)
			}
			messages = append(messages, aMsg{
				Role: "user",
				Content: []map[string]interface{}{{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     resultContent,
				}},
			})

		default:
			if len(m.ContentParts) > 0 {
				messages = append(messages, aMsg{
					Role:    m.Role,
					Content: contentPartsToAnthropicBlocks(m.ContentParts),
				})
			} else {
				text := ""
				if m.Content != nil {
					text = *m.Content
				}
				messages = append(messages, aMsg{
					Role:    m.Role,
					Content: []map[string]interface{}{{"type": "text", "text": text}},
				})
			}
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

func contentPartsToAnthropicBlocks(parts []ContentPart) []map[string]interface{} {
	blocks := make([]map[string]interface{}, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, map[string]interface{}{"type": "text", "text": part.Text})
		case "image_url":
			if part.ImageURL != nil {
				mediaType, data := ParseDataURI(part.ImageURL.URL)
				blocks = append(blocks, map[string]interface{}{
					"type": "image",
					"source": map[string]interface{}{
						"type":       "base64",
						"media_type": mediaType,
						"data":       data,
					},
				})
			}
		}
	}
	return blocks
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

func anthropicBlocksToMessage(role string, blocks []anthropicContentBlock) ChatMessage {
	if role == "" {
		role = "assistant"
	}
	var text, thinking strings.Builder
	toolCalls := make([]ToolCall, 0)
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := anthropicToolArguments(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}

	msg := ChatMessage{Role: role}
	if content := thinking.String(); content != "" {
		msg.ReasoningContent = &content
	}
	if content := text.String(); content != "" {
		msg.Content = &content
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
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

func convertAnthropicUsage(usage *anthropicUsage) *Usage {
	if usage == nil {
		return nil
	}
	promptTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	completionTokens := usage.OutputTokens
	return &Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		CacheReadTokens:  usage.CacheReadInputTokens,
		CacheWriteTokens: usage.CacheCreationInputTokens,
	}
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
			Delta: ChatMessageDelta{Role: role},
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
			text := event.ContentBlock.Text
			return ChatCompletionStreamEvent{Delta: ChatMessageDelta{Content: &text}}, nil
		case "tool_use":
			args := anthropicToolArguments(event.ContentBlock.Input)
			delta := ToolCallDelta{
				Index: event.Index,
				ID:    event.ContentBlock.ID,
				Type:  "function",
				Function: FunctionCallDelta{
					Name: event.ContentBlock.Name,
				},
			}
			if args != "{}" {
				delta.Function.Arguments = args
			}
			return ChatCompletionStreamEvent{
				Delta: ChatMessageDelta{
					ToolCalls: []ToolCallDelta{delta},
				},
			}, nil
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
			text := event.Delta.Text
			return ChatCompletionStreamEvent{Delta: ChatMessageDelta{Content: &text}}, nil
		case "input_json_delta":
			return ChatCompletionStreamEvent{
				Delta: ChatMessageDelta{
					ToolCalls: []ToolCallDelta{{
						Index: event.Index,
						Function: FunctionCallDelta{
							Arguments: event.Delta.PartialJSON,
						},
					}},
				},
			}, nil
		case "thinking_delta":
			thinking := event.Delta.Thinking
			return ChatCompletionStreamEvent{Delta: ChatMessageDelta{ReasoningContent: &thinking}}, nil
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

func (p *anthropicStreamParser) usageSnapshot() *Usage {
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
