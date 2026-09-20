//go:build live_llm

package harness_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Forward real completions, buffering each SSE response until every tool call
// has been validated. No assistant text/tool argument is generated or corrected
// here. Long tasks also expose the application's workspace file tools.
type subagentGateway struct {
	server            *httptest.Server
	config            map[string]any
	space, nonce      string
	mu                sync.Mutex
	requests          int
	spawned           map[string]bool
	roles             map[string]int
	parentCompletions map[string]bool
	peerInputs        map[string]bool
	log               *os.File
	failure           chan error
	longTask          bool
	gameTask          bool
	projectRoot       string
	outputTokens      int
	usageMissing      int
}

func newSubagentGateway(t *testing.T, dir, space, nonce string) *subagentGateway {
	return startSubagentGateway(t, dir, space, nonce, false)
}

func startSubagentGateway(t *testing.T, dir, space, nonce string, longTask bool) *subagentGateway {
	t.Helper()
	cfg := liveLLMRequest(t)
	if cfg["provider"] != "openai" && cfg["provider"] != "deepseek" {
		t.Fatal("subagent harness requires OpenAI-compatible completions")
	}
	f, err := os.Create(filepath.Join(dir, "subagent-model.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	g := &subagentGateway{config: cfg, space: space, nonce: nonce, spawned: make(map[string]bool), roles: make(map[string]int), peerInputs: make(map[string]bool), parentCompletions: make(map[string]bool), log: f, failure: make(chan error, 1)}
	g.longTask = longTask
	g.projectRoot = filepath.Join(dir, "project")
	lifetime := 160 * time.Second
	if longTask {
		lifetime = 60 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), lifetime)
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { g.serve(ctx, w, r) }))
	t.Cleanup(func() {
		cancel()
		g.server.Close()
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}

func (g *subagentGateway) record(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err = g.log.Write(append(redactSecrets(data), '\n'))
	return err
}
func (g *subagentGateway) reject(w http.ResponseWriter, err error) {
	select {
	case g.failure <- err:
	default:
	}
	_ = g.record(map[string]any{"rejected": err.Error()})
	http.Error(w, "harness rejected model exchange", http.StatusForbidden)
}

func (g *subagentGateway) serve(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/chat/completions" {
		g.reject(w, fmt.Errorf("unexpected provider endpoint"))
		return
	}
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
		g.reject(w, err)
		return
	}
	g.mu.Lock()
	g.requests++
	requestID := g.requests
	g.mu.Unlock()
	limit, tokenLimit, callTimeout := 40, 1024, 35*time.Second
	if g.longTask {
		limit, tokenLimit, callTimeout = 200, 16384, 5*time.Minute
	}
	if requestID > limit {
		g.reject(w, fmt.Errorf("global limit of %d model requests exceeded", limit))
		return
	}
	role := "probe"
	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		role = requestAgentRole(body)
		if g.longTask {
			role = longTaskRole(body)
		}
		if g.gameTask {
			role = longRequestSession(body)
			if role != "black" && role != "white" {
				g.reject(w, fmt.Errorf("game only has black and white players, got %q", role))
				return
			}
		}
		if role == "" {
			g.reject(w, fmt.Errorf("unidentified Agent request"))
			return
		}
		var restricted []any
		for _, definition := range tools {
			item, ok := definition.(map[string]any)
			if !ok {
				continue
			}
			name, _ := field(item, "function", "name").(string)
			if name == "bash" || (g.gameTask && name == "inbox_wait") || (name == "subagent" && role == "parent") ||
				(g.longTask && (name == "read" || name == "ls" || name == "glob" || name == "write")) {
				restricted = append(restricted, item)
			}
		}
		body["tools"] = restricted
		g.mu.Lock()
		g.roles[role]++
		for _, text := range []string{"offer:" + g.nonce, "reply:" + g.nonce, "ack:" + g.nonce} {
			if requestPeerContains(body, text) {
				g.peerInputs[role+":"+strings.Split(text, ":")[0]] = true
			}
		}
		if role == "parent" {
			for _, name := range []string{"worker-a", "worker-b"} {
				if requestCompletionContains(body, `<subagent_completion name="" label="`+name+`"`) {
					g.parentCompletions[name] = true
				}
			}
		}
		g.mu.Unlock()
	} else {
		if !requestContains(body, "ping") {
			g.reject(w, fmt.Errorf("unexpected non-Agent model request"))
			return
		}
	}
	if n, ok := body["max_tokens"].(float64); !ok || n > float64(tokenLimit) {
		body["max_tokens"] = tokenLimit
	}
	data, err := json.Marshal(body)
	if err != nil {
		g.reject(w, err)
		return
	}
	if err = g.record(map[string]any{"request": requestID, "role": role, "body": body}); err != nil {
		g.reject(w, err)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	req, err := http.NewRequestWithContext(callCtx, "POST", strings.TrimRight(g.config["baseUrl"].(string), "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		g.reject(w, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.config["apiKey"].(string))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			g.recordCanceledRequest(w, requestID, role, nil, tokenLimit, false)
			return
		}
		g.reject(w, fmt.Errorf("upstream: %s", redactSecrets([]byte(err.Error()))))
		return
	}
	responseLimit := int64(4 << 20)
	if g.longTask {
		responseLimit = 16 << 20
	}
	response, readErr := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	resp.Body.Close()
	if readErr != nil || int64(len(response)) > responseLimit {
		if readErr != nil && r.Context().Err() != nil {
			stream, _ := body["stream"].(bool)
			g.recordCanceledRequest(w, requestID, role, response, tokenLimit, stream)
			return
		}
		_ = g.record(map[string]any{"partial_response": requestID, "role": role, "body": string(response), "read_error": fmt.Sprint(readErr)})
		if g.longTask {
			g.mu.Lock()
			g.outputTokens += tokenLimit
			g.usageMissing++
			g.mu.Unlock()
		}
		g.reject(w, fmt.Errorf("invalid or oversized upstream response: %v", readErr))
		return
	}
	if resp.StatusCode != 200 {
		g.reject(w, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, redactSecrets(response)))
		return
	}
	stream, _ := body["stream"].(bool)
	// Preserve and charge responses even when their tool calls are rejected.
	if err = g.record(map[string]any{"response": requestID, "role": role, "stream": stream, "body": string(response)}); err != nil {
		g.reject(w, err)
		return
	}
	maxCalls := 2
	if g.longTask {
		maxCalls = 8
	}
	if g.gameTask {
		maxCalls = 64
	}
	var calls []gatewayCall
	// Startup ping intentionally uses a tiny token cap and performs no Agent work.
	// A truncated ping must not be classified as a truncated task response.
	if role != "probe" {
		calls, err = completionToolCalls(response, stream, maxCalls)
	}
	if err == nil {
		if g.gameTask {
			// Game rules live in the task, not in a scripted tool sequence.
			// The ordinary application executes the model's actual calls.
		} else if g.longTask {
			err = validateLongTaskCalls(role, calls, g.projectRoot)
		} else {
			err = g.validateCalls(role, calls)
		}
	}
	if g.longTask {
		tokens, found := responseOutputTokens(response, stream)
		g.mu.Lock()
		if !found {
			tokens = tokenLimit
			g.usageMissing++
		}
		g.outputTokens += tokens
		exceeded := g.outputTokens > 120000
		g.mu.Unlock()
		if exceeded {
			g.reject(w, fmt.Errorf("120000 output token budget exceeded (missing usage charged at maximum)"))
			return
		}
	}
	if err != nil {
		g.reject(w, fmt.Errorf("%s response: %w", role, err))
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	_, _ = w.Write(response)
}

// Canceling a model request is expected when an Agent is interrupted or killed.
// Preserve evidence and conservatively charge missing usage, without treating
// the caller's cancellation itself as a provider or harness failure.
func (g *subagentGateway) recordCanceledRequest(w http.ResponseWriter, id int, role string, response []byte, maximum int, stream bool) {
	tokens, found := responseOutputTokens(response, stream)
	if !found {
		tokens = maximum
	}
	if err := g.record(map[string]any{"canceled_request": id, "role": role, "partial_response_body": string(response), "output_tokens_charged": tokens, "usage_missing": !found}); err != nil {
		g.reject(w, err)
		return
	}
	if !g.longTask {
		return
	}
	g.mu.Lock()
	g.outputTokens += tokens
	if !found {
		g.usageMissing++
	}
	exceeded := g.outputTokens > 120000
	g.mu.Unlock()
	if exceeded {
		g.reject(w, fmt.Errorf("120000 output token budget exceeded while accounting for canceled requests"))
	}
}

func requestAgentRole(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for _, value := range messages {
		m, ok := value.(map[string]any)
		if !ok || m["role"] != "user" {
			continue
		}
		text := chatText(m["content"])
		for prefix, role := range map[string]string{"HARNESS_PARENT": "parent", "HARNESS_A": "a", "HARNESS_B": "b"} {
			if strings.HasPrefix(strings.TrimSpace(text), prefix) {
				return role
			}
		}
		return ""
	}
	return ""
}
func chatText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	var result strings.Builder
	if parts, ok := content.([]any); ok {
		for _, part := range parts {
			if m, ok := part.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					result.WriteString(s)
				}
			}
		}
	}
	return result.String()
}
func requestContains(body map[string]any, needle string) bool {
	messages, _ := body["messages"].([]any)
	for _, value := range messages {
		if m, ok := value.(map[string]any); ok && strings.Contains(chatText(m["content"]), needle) {
			return true
		}
	}
	return false
}

// Completion evidence must be supplied by the application's system inbox, not
// quoted or invented in an assistant response or tool result.
func requestCompletionContains(body map[string]any, needle string) bool {
	messages, _ := body["messages"].([]any)
	for _, value := range messages {
		if m, ok := value.(map[string]any); ok && m["role"] == "user" {
			text := chatText(m["content"])
			if strings.HasPrefix(text, `<message origin="system"`) && strings.Contains(text, needle) {
				return true
			}
		}
	}
	return false
}

// Only user messages supplied by the IOA Inbox count as delivery evidence.
func requestPeerContains(body map[string]any, needle string) bool {
	messages, _ := body["messages"].([]any)
	for _, value := range messages {
		if m, ok := value.(map[string]any); ok && m["role"] == "user" {
			text := chatText(m["content"])
			if strings.HasPrefix(text, `<message origin="peer"`) && strings.Contains(text, needle) {
				return true
			}
		}
	}
	return false
}

type gatewayCall struct {
	Index    int `json:"index"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func completionToolCalls(data []byte, stream bool, maxCalls int) ([]gatewayCall, error) {
	if !stream {
		var response struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Calls []gatewayCall `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		err := json.Unmarshal(data, &response)
		if err != nil {
			return nil, err
		}
		if len(response.Choices) != 1 {
			return nil, fmt.Errorf("one completion required")
		}
		if response.Choices[0].FinishReason == "length" || response.Choices[0].FinishReason == "max_tokens" {
			return nil, fmt.Errorf("model response truncated by output token limit")
		}
		if len(response.Choices[0].Message.Calls) > maxCalls {
			return nil, fmt.Errorf("too many tool calls")
		}
		return response.Choices[0].Message.Calls, nil
	}
	calls := map[int]*gatewayCall{}
	done := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		frame := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if frame == "[DONE]" {
			done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Calls []gatewayCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(frame), &chunk); err != nil {
			return nil, err
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason == "length" || choice.FinishReason == "max_tokens" {
				return nil, fmt.Errorf("model response truncated by output token limit")
			}
			if choice.Index != 0 {
				return nil, fmt.Errorf("multiple choices")
			}
			for _, delta := range choice.Delta.Calls {
				if delta.Index < 0 || delta.Index >= maxCalls {
					return nil, fmt.Errorf("too many tool calls")
				}
				call := calls[delta.Index]
				if call == nil {
					call = &gatewayCall{Index: delta.Index}
					calls[delta.Index] = call
				}
				call.Function.Name += delta.Function.Name
				call.Function.Arguments += delta.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !done {
		return nil, fmt.Errorf("incomplete SSE completion")
	}
	var out []gatewayCall
	for i := 0; i < len(calls); i++ {
		call, ok := calls[i]
		if !ok {
			return nil, fmt.Errorf("noncontiguous tool indices")
		}
		out = append(out, *call)
	}
	return out, nil
}

func (g *subagentGateway) validateCalls(role string, calls []gatewayCall) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, call := range calls {
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return err
		}
		switch call.Function.Name {
		case "subagent":
			if role != "parent" {
				return fmt.Errorf("nested delegation forbidden")
			}
			action, _ := args["action"].(string)
			if action == "list" {
				continue
			}
			if action != "" && action != "create" {
				return fmt.Errorf("only create/list allowed")
			}
			name, _ := args["label"].(string)
			prompt, _ := args["prompt"].(string)
			if (name != "worker-a" && name != "worker-b") || g.spawned[name] || args["mode"] != "async" {
				return fmt.Errorf("expected one async delegation per worker")
			}
			if typ, _ := args["name"].(string); typ != "" {
				return fmt.Errorf("only anonymous subagents allowed")
			}
			prefix := "HARNESS_A"
			if name == "worker-b" {
				prefix = "HARNESS_B"
				if strings.Contains(prompt, g.nonce) {
					return fmt.Errorf("B must discover the nonce through IOA")
				}
			}
			if !strings.HasPrefix(prompt, prefix) || len(prompt) > 4000 {
				return fmt.Errorf("missing bounded child task marker")
			}
			g.spawned[name] = true
		case "bash":
			command, _ := args["command"].(string)
			if err := g.validateCommand(role, command); err != nil {
				return err
			}
			for key, value := range args {
				switch key {
				case "command":
				case "wait":
					if value != float64(0) {
						return fmt.Errorf("background shell wait forbidden")
					}
				case "timeout":
					if n, ok := value.(float64); !ok || n <= 0 || n > 10 {
						return fmt.Errorf("command timeout must be 1-10 seconds")
					}
				default:
					return fmt.Errorf("unknown bash option %s", key)
				}
			}
		default:
			return fmt.Errorf("tool %q is outside the scenario", call.Function.Name)
		}
	}
	return nil
}

func (g *subagentGateway) validateCommand(role, command string) error {
	if role == "parent" && command == "ioa space "+g.space+" harness" {
		return nil
	}
	if role != "parent" && role != "a" && role != "b" {
		return fmt.Errorf("unidentified IOA caller")
	}
	if command == "ioa space nodes" || (role != "b" && command == "ioa read --all --limit 20") || regexp.MustCompile(`^ioa read --all --message [a-f0-9]{20,40} --direction downstream$`).MatchString(command) {
		return nil
	}
	texts := []string{}
	if role == "a" {
		texts = []string{"offer:" + g.nonce, "ack:" + g.nonce}
	}
	if role == "b" {
		texts = []string{"reply:" + g.nonce}
	}
	for _, text := range texts {
		prefix := `ioa send --content '{"text":"` + text + `"}'`
		if regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + ` --target-session [a-f0-9]{16,40}( --ref-messages [a-f0-9]{20,40})?$`).MatchString(command) {
			return nil
		}
	}
	return fmt.Errorf("IOA command outside finite scenario: %q", command)
}
