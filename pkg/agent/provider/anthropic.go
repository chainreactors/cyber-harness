package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chainreactors/proxyclient"
)

const (
	defaultAnthropicTimeout  = 120 * time.Second
	defaultAnthropicMaxToken = 4096
	anthropicVersion         = "2023-06-01"
)

type AnthropicProvider struct {
	config *ProviderConfig
	client *http.Client
}

func NewAnthropicProvider(cfg *ProviderConfig) (*AnthropicProvider, error) {
	timeout := anthropicTimeout(cfg.Timeout)

	transport := &http.Transport{
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
	}

	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		dial, err := proxyclient.NewClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("create proxy client: %w", err)
		}
		transport.DialContext = dial.DialContext
	}

	client := &http.Client{
		Transport: transport,
	}

	return &AnthropicProvider{config: cfg, client: client}, nil
}

func (p *AnthropicProvider) Name() string {
	return p.config.Provider
}

func (p *AnthropicProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false

	parentCtx := ctx
	callTimeout := anthropicTimeout(p.config.Timeout)
	var callTimedOut atomic.Bool
	if callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		timer := time.AfterFunc(callTimeout, func() {
			callTimedOut.Store(true)
			cancel()
		})
		defer func() {
			timer.Stop()
			cancel()
		}()
	}

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := p.completionEndpoint()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setRequestHeaders(httpReq, false)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), callTimeout, "http request", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), callTimeout, "read response", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	result, err := parseAnthropicResponse(respBody)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			apiErr.StatusCode = resp.StatusCode
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

	bodyBytes, err := p.marshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, reqCancel := context.WithCancel(ctx)

	endpoint := p.completionEndpoint()
	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		reqCancel()
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setRequestHeaders(httpReq, true)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		reqCancel()
		return nil, fmt.Errorf("http request: %w", err)
	}
	closeBody := true
	defer func() {
		if closeBody {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer reqCancel()
		timeout := anthropicTimeout(p.config.Timeout)
		respBody, timedOut, err := readAllWithCancelTimeout(resp.Body, reqCancel, timeout)
		if err != nil {
			return nil, wrapReadError(ctx, timedOut, timeout, "read response", err)
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	stallTimeout := anthropicTimeout(p.config.Timeout)
	var stallDetected atomic.Bool
	stallTimer := time.AfterFunc(stallTimeout, func() {
		stallDetected.Store(true)
		reqCancel()
	})

	events := make(chan ChatCompletionStreamEvent)
	closeBody = false // ownership transferred to goroutine
	go func() {
		defer reqCancel()
		defer resp.Body.Close()
		defer close(events)
		defer stallTimer.Stop()

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		parser := &anthropicStreamParser{}
		var sseEvent string

		for scanner.Scan() {
			stallTimer.Reset(stallTimeout)

			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "event:") {
				sseEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				select {
				case events <- ChatCompletionStreamEvent{Done: true}:
				case <-ctx.Done():
				}
				return
			}

			event, err := parser.parse(sseEvent, []byte(data))
			sseEvent = ""
			if err != nil {
				select {
				case events <- ChatCompletionStreamEvent{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			if event.Done {
				select {
				case events <- event:
				case <-ctx.Done():
				}
				return
			}
			if event.Err != nil || event.Delta.Role != "" || event.Delta.Content != nil || event.Delta.ReasoningContent != nil || len(event.Delta.ToolCalls) > 0 || event.FinishReason != "" || event.Usage != nil {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			streamErr := fmt.Errorf("read stream: %w", err)
			if stallDetected.Load() {
				streamErr = fmt.Errorf("%w (no data for %s): %v", ErrStreamStalled, stallTimeout, err)
			}
			select {
			case events <- ChatCompletionStreamEvent{Err: streamErr}:
			case <-ctx.Done():
			}
			return
		}

		select {
		case events <- ChatCompletionStreamEvent{Done: true}:
		case <-ctx.Done():
		}
	}()

	return events, nil
}

func (p *AnthropicProvider) completionEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	return base + "/messages"
}

func (p *AnthropicProvider) setRequestHeaders(req *http.Request, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if p.config.APIKey != "" {
		req.Header.Set("x-api-key", p.config.APIKey)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
}

// marshalRequest serializes req to JSON for the Anthropic Messages API:
//  1. Tools: type:"function" -> type:"custom", parameters -> input_schema
//  2. System messages: extracted to top-level "system" field
//  3. Assistant tool_calls -> tool_use content blocks
//  4. Tool-role messages -> user-role with tool_result content blocks
func (p *AnthropicProvider) marshalRequest(req *ChatCompletionRequest) ([]byte, error) {
	type anthropicTool struct {
		Type        string                 `json:"type"`
		Name        string                 `json:"name"`
		Description string                 `json:"description,omitempty"`
		InputSchema map[string]interface{} `json:"input_schema"`
	}

	var tools []anthropicTool
	for _, t := range req.Tools {
		inputSchema := t.Function.Parameters
		if inputSchema == nil {
			inputSchema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		tools = append(tools, anthropicTool{
			Type:        "custom",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: inputSchema,
		})
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
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxToken
	}

	wrapper := struct {
		Model       string          `json:"model"`
		Messages    []aMsg          `json:"messages"`
		System      string          `json:"system,omitempty"`
		Tools       []anthropicTool `json:"tools,omitempty"`
		MaxTokens   int             `json:"max_tokens,omitempty"`
		Temperature *float64        `json:"temperature,omitempty"`
		Stream      bool            `json:"stream,omitempty"`
	}{
		Model:       req.Model,
		Messages:    merged,
		System:      strings.Join(systemParts, "\n\n"),
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
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
	var text strings.Builder
	toolCalls := make([]ToolCall, 0)
	for _, block := range blocks {
		switch block.Type {
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
		return ChatCompletionStreamEvent{}, fmt.Errorf("API error: [%s] %s", probe.Error.Type, probe.Error.Message)
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
		return ChatCompletionStreamEvent{}, fmt.Errorf("API error: [%s] %s", probe.Error.Type, probe.Error.Message)

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

func anthropicTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultAnthropicTimeout
	}
	return time.Duration(seconds) * time.Second
}
