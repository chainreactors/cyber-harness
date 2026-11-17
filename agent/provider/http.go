package provider

import (
	"bufio"
	"bytes"
	"context"
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

const defaultTimeout = 120 * time.Second

func timeoutFromConfig(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func (r *ChatCompletionRequest) timeout(seconds int) time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return timeoutFromConfig(seconds)
}

func newHTTPClient(cfg *ProviderConfig) (*http.Client, error) {
	transport := &http.Transport{
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
	return &http.Client{Transport: transport}, nil
}

type apiRequest struct {
	client  *http.Client
	timeout time.Duration
}

func (r *apiRequest) do(ctx context.Context, method, endpoint string, body []byte, setHeaders func(*http.Request)) ([]byte, error) {
	parentCtx := ctx
	var callTimedOut atomic.Bool
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		timer := time.AfterFunc(r.timeout, func() { callTimedOut.Store(true); cancel() })
		defer func() { timer.Stop(); cancel() }()
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if setHeaders != nil {
		setHeaders(req)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), r.timeout, "http request", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapReadError(parentCtx, callTimedOut.Load(), r.timeout, "read response", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: string(data), Header: resp.Header.Clone()}
	}
	return data, nil
}

// hint404 enriches a 404 from a chat endpoint with an actionable cause. A 404 on
// the chat POST almost always means the path does not exist: a wrong base_url,
// or — most often with third-party gateways — a protocol mismatch, where the
// endpoint does not speak this provider's wire protocol (e.g. an Anthropic-only
// gateway has no /chat/completions). The underlying *APIError is preserved via
// %w, so retry classification and errors.As stay intact.
func hint404(err error, endpoint, altProtocol, altProvider string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		return err
	}
	return fmt.Errorf("%w — POST %s not found (404); verify llm.base_url, or if this gateway speaks the %s protocol set llm.provider=%s",
		err, endpoint, altProtocol, altProvider)
}

// streamSSE opens an SSE connection and returns a channel of parsed events.
// parse is called for each data line with the preceding event type (may be empty).
func streamSSE(
	ctx context.Context,
	client *http.Client,
	timeout time.Duration,
	endpoint string,
	body []byte,
	setHeaders func(*http.Request),
	providerName string,
	protocol string,
	acceptDoneMarker bool,
	parse func(eventType string, data []byte) ([]ChatCompletionStreamEvent, error),
) (<-chan ChatCompletionStreamEvent, error) {
	captureFrame(ctx, RawFrame{Provider: providerName, Protocol: protocol, Direction: "request", Transport: "http", Payload: body, MediaType: "application/json"})
	reqCtx, reqCancel := context.WithCancel(ctx)

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		reqCancel()
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if setHeaders != nil {
		setHeaders(httpReq)
	}

	// The request owns its fallback, including the wait for response headers.
	// A transport-wide deadline would cap longer background requests.
	var stallDetected atomic.Bool
	stallTimer := time.AfterFunc(timeout, func() {
		stallDetected.Store(true)
		reqCancel()
	})
	resp, err := client.Do(httpReq) //nolint:bodyclose // closed in goroutine below
	if err != nil {
		stallTimer.Stop()
		reqCancel()
		return nil, wrapReadError(ctx, stallDetected.Load(), timeout, "http request", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer stallTimer.Stop()
		defer resp.Body.Close()
		defer reqCancel()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, wrapReadError(ctx, stallDetected.Load(), timeout, "read response", readErr)
		}
		captureFrame(ctx, RawFrame{Provider: providerName, Protocol: protocol, EventType: "error", Direction: "response", Transport: "sse", Payload: respBody, MediaType: "application/json"})
		return nil, &APIError{StatusCode: resp.StatusCode, Message: string(respBody), Header: resp.Header.Clone()}
	}

	stallTimer.Reset(timeout)

	events := make(chan ChatCompletionStreamEvent, 32)
	go func() {
		defer reqCancel()
		defer resp.Body.Close()
		defer close(events)
		defer stallTimer.Stop()

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		var sseEvent string
		for scanner.Scan() {
			stallTimer.Reset(timeout)

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
			rawLine := scanner.Bytes()
			colon := bytes.Index(rawLine, []byte("data:"))
			rawData := rawLine[colon+len("data:"):]
			if len(rawData) > 0 && rawData[0] == ' ' {
				rawData = rawData[1:]
			}
			captureFrame(ctx, RawFrame{Provider: providerName, Protocol: protocol, EventType: sseEvent, Direction: "response", Transport: "sse", Payload: rawData, MediaType: "application/json"})
			data := strings.TrimSpace(string(rawData))
			if data == "[DONE]" {
				if !acceptDoneMarker {
					sseSend(ctx, events, ChatCompletionStreamEvent{Err: ErrStreamIncomplete})
					return
				}
				sseSend(ctx, events, ChatCompletionStreamEvent{Done: true})
				return
			}

			parsed, parseErr := parse(sseEvent, []byte(data))
			sseEvent = ""
			if parseErr != nil {
				sseSend(ctx, events, ChatCompletionStreamEvent{Err: parseErr})
				return
			}
			for _, event := range parsed {
				if event.Done {
					sseSend(ctx, events, event)
					return
				}
				if event.Role != "" || event.MessageDelta != nil ||
					len(event.ToolDeltas) > 0 ||
					event.FinishReason != "" || event.Usage != nil {
					select {
					case events <- event:
					case <-ctx.Done():
						return
					}
				}
			}
		}

		if scanErr := scanner.Err(); scanErr != nil {
			streamErr := fmt.Errorf("read stream: %w", scanErr)
			if stallDetected.Load() {
				streamErr = fmt.Errorf("%w (no data for %s): %v", ErrStreamStalled, timeout, scanErr)
			}
			sseSend(ctx, events, ChatCompletionStreamEvent{Err: streamErr})
			return
		}

		if acceptDoneMarker {
			sseSend(ctx, events, ChatCompletionStreamEvent{Done: true})
		} else {
			sseSend(ctx, events, ChatCompletionStreamEvent{Err: ErrStreamIncomplete})
		}
	}()

	return events, nil
}

func captureAPIErrorFrame(ctx context.Context, providerName, protocol string, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return
	}
	captureFrame(ctx, RawFrame{
		Provider: providerName, Protocol: protocol, EventType: "error", Direction: "response",
		Transport: "http", Payload: []byte(apiErr.Message), MediaType: "application/json",
	})
}

func sseSend(ctx context.Context, ch chan<- ChatCompletionStreamEvent, event ChatCompletionStreamEvent) {
	select {
	case ch <- event:
	case <-ctx.Done():
	}
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
