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
	defaultOpenAITimeout = 120 * time.Second
)

type OpenAIProvider struct {
	config *ProviderConfig
	client *http.Client
}

func NewOpenAIProvider(cfg *ProviderConfig) (*OpenAIProvider, error) {
	timeout := openAITimeout(cfg.Timeout)

	transport := &http.Transport{
		// ResponseHeaderTimeout caps how long we wait for the server to
		// begin sending response headers after the request is fully written.
		// This catches the "deepseek accepted the request but never starts
		// responding" case without putting a total lifetime cap on healthy
		// streaming responses.
		ResponseHeaderTimeout: timeout,

		// IdleConnTimeout closes idle keep-alive connections that sit unused.
		// Prevents stale connections from being reused after a server-side
		// reset or network interruption.
		IdleConnTimeout: 90 * time.Second,
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

	// Do NOT set http.Client.Timeout: it covers the entire lifecycle,
	// including body reads, and kills long streaming responses. Instead we
	// rely on request-scoped cancellation plus the Transport timeouts above.
	client := &http.Client{
		Transport: transport,
	}

	return &OpenAIProvider{config: cfg, client: client}, nil
}

func (p *OpenAIProvider) Name() string {
	return p.config.Provider
}

func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = false

	// Per-call timeout: bounds the entire non-streaming request+response.
	// This catches deepseek accepting a connection then stalling before
	// finishing the response body without reintroducing http.Client.Timeout
	// for streaming calls.
	parentCtx := ctx
	callTimeout := openAITimeout(p.config.Timeout)
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

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := p.completionEndpoint()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setRequestHeaders(httpReq, false)

	resp, err := p.client.Do(httpReq) //nolint:bodyclose // closed by the stream reader goroutine, or on non-2xx below.
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), callTimeout, "http request", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), callTimeout, "read response", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result ChatCompletionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("API error: [%s] %s", result.Error.Type, result.Error.Message)
	}

	return &result, nil
}

func (p *OpenAIProvider) ChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	if req.Model == "" {
		req.Model = p.config.Model
	}
	req.Stream = true

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Derive a cancellable context for this request.  The stall detector
	// below cancels reqCtx (not the caller's ctx) to tear down the TCP
	// connection and unblock body reads when the server stops sending data.
	reqCtx, reqCancel := context.WithCancel(ctx)

	endpoint := p.completionEndpoint()
	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		reqCancel()
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setRequestHeaders(httpReq, true)

	//nolint:bodyclose // The stream response body is closed by the reader goroutine, or on non-2xx below.
	resp, err := p.client.Do(httpReq)
	if err != nil {
		reqCancel()
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		defer reqCancel()
		respBody, timedOut, err := readAllWithCancelTimeout(resp.Body, reqCancel, openAITimeout(p.config.Timeout))
		if err != nil {
			return nil, wrapReadError(ctx, timedOut, openAITimeout(p.config.Timeout), "read response", err)
		}
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Stall detection: if no SSE data arrives within stallTimeout, cancel
	// reqCtx to close the TCP connection and unblock resp.Body reads.
	// This is the core fix for the "deepseek returns partial SSE then
	// hangs" scenario. ResponseHeaderTimeout no longer applies after body
	// reads begin, and http.Client.Timeout would be a total cap rather than
	// an idle-stream watchdog.
	stallTimeout := openAITimeout(p.config.Timeout)
	var stallDetected atomic.Bool
	stallTimer := time.AfterFunc(stallTimeout, func() {
		stallDetected.Store(true)
		reqCancel()
	})

	events := make(chan ChatCompletionStreamEvent)
	go func() {
		defer reqCancel()
		defer resp.Body.Close()
		defer close(events)
		defer stallTimer.Stop()

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			// Any data from the server resets the stall timer.
			stallTimer.Reset(stallTimeout)

			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
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

			event, err := parseOpenAIStreamChunk([]byte(data))
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
			if event.Err != nil || event.Done || event.Delta.Role != "" || event.Delta.Content != nil || event.Delta.ReasoningContent != nil || len(event.Delta.ToolCalls) > 0 || event.FinishReason != "" || event.Usage != nil {
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			// Distinguish stall-induced cancellation from user/parent cancellation.
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

func (p *OpenAIProvider) completionEndpoint() string {
	base := strings.TrimSuffix(p.config.BaseURL, "/")
	return base + "/chat/completions"
}

func (p *OpenAIProvider) setRequestHeaders(req *http.Request, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
}

func openAITimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultOpenAITimeout
	}
	return time.Duration(seconds) * time.Second
}

func readAllWithCancelTimeout(r io.Reader, cancel context.CancelFunc, timeout time.Duration) ([]byte, bool, error) {
	var timedOut atomic.Bool
	timer := time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer timer.Stop()

	body, err := io.ReadAll(r)
	return body, timedOut.Load(), err
}

func wrapReadError(parentCtx context.Context, timedOut bool, timeout time.Duration, op string, err error) error {
	if timedOut && parentCtx.Err() == nil {
		return fmt.Errorf("%s: %w after %s: %v", op, ErrCallTimeout, timeout, err)
	}
	if errors.Is(err, context.Canceled) && parentCtx.Err() == nil {
		return fmt.Errorf("%s: %w", op, ErrCallTimeout)
	}
	return fmt.Errorf("%s: %w", op, err)
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
		return ChatCompletionStreamEvent{}, fmt.Errorf("API error: [%s] %s", chunk.Error.Type, chunk.Error.Message)
	}
	event := ChatCompletionStreamEvent{Usage: chunk.Usage}
	if len(chunk.Choices) == 0 {
		return event, nil
	}
	event.Delta = chunk.Choices[0].Delta
	event.FinishReason = chunk.Choices[0].FinishReason
	return event, nil
}
