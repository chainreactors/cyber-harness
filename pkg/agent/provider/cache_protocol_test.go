package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// cachedPrefix tracks requests to compute realistic server-side cache behavior.
type cachedPrefix struct {
	mu     sync.Mutex
	cached int // tokens "cached" on server side
}

func (c *cachedPrefix) hit(promptTokens int) (cacheRead, cacheWrite int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached > 0 && c.cached <= promptTokens {
		cacheRead = c.cached
	} else {
		// First request: write to cache (use ~80% of prompt as cache)
		cacheWrite = promptTokens * 80 / 100
		c.cached = cacheWrite
	}
	return
}

// --- OpenAI mock server ---

func newOpenAIMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50 // rough estimate

		stream, _ := req["stream"].(bool)

		// Check cache fields arrive correctly
		cacheKey, _ := req["prompt_cache_key"].(string)
		cacheRet, _ := req["prompt_cache_retention"].(string)
		t.Logf("[OpenAI turn %d] messages=%d prompt_cache_key=%q prompt_cache_retention=%q stream=%v",
			turn, len(messages), cacheKey, cacheRet, stream)

		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 10

		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)

			// Content chunk
			contentChunk := fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","content":"response turn %d"},"index":0}]}`, turn)
			fmt.Fprintf(w, "data: %s\n\n", contentChunk)

			// Final chunk with usage
			usageChunk := fmt.Sprintf(
				`{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d,"cache_write_tokens":%d}}}`,
				promptTokens, completionTokens, promptTokens+completionTokens, cacheRead, cacheWrite)
			fmt.Fprintf(w, "data: %s\n\n", usageChunk)
			fmt.Fprintf(w, "data: [DONE]\n\n")
		} else {
			resp := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", turn),
				"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": fmt.Sprintf("response turn %d", turn)}, "finish_reason": "stop"}},
				"usage": map[string]interface{}{
					"prompt_tokens":     promptTokens,
					"completion_tokens": completionTokens,
					"total_tokens":      promptTokens + completionTokens,
					"prompt_tokens_details": map[string]interface{}{
						"cached_tokens":      cacheRead,
						"cache_write_tokens": cacheWrite,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

// --- Anthropic mock server ---

func newAnthropicMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "not found", 404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50

		// Verify cache_control markers are present
		systemVal := req["system"]
		hasSysCacheCtrl := false
		if sysArr, ok := systemVal.([]interface{}); ok {
			for _, s := range sysArr {
				block := s.(map[string]interface{})
				if _, ok := block["cache_control"]; ok {
					hasSysCacheCtrl = true
				}
			}
		}

		hasToolCacheCtrl := false
		if toolsVal, ok := req["tools"].([]interface{}); ok && len(toolsVal) > 0 {
			lastTool := toolsVal[len(toolsVal)-1].(map[string]interface{})
			_, hasToolCacheCtrl = lastTool["cache_control"]
		}

		hasUserCacheCtrl := false
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i].(map[string]interface{})
			if msg["role"] == "user" {
				blocks := msg["content"].([]interface{})
				lastBlock := blocks[len(blocks)-1].(map[string]interface{})
				_, hasUserCacheCtrl = lastBlock["cache_control"]
				break
			}
		}

		isStream, _ := req["stream"].(bool)

		t.Logf("[Anthropic turn %d] messages=%d cache_control: system=%v tools=%v last_user=%v stream=%v",
			turn, len(messages), hasSysCacheCtrl, hasToolCacheCtrl, hasUserCacheCtrl, isStream)

		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 10

		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)

			// message_start with usage
			fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "message_start",
				"message": map[string]interface{}{
					"id":   fmt.Sprintf("msg_%d", turn),
					"role": "assistant",
					"usage": map[string]interface{}{
						"input_tokens":                promptTokens,
						"output_tokens":               0,
						"cache_creation_input_tokens":  cacheWrite,
						"cache_read_input_tokens":      cacheRead,
					},
				},
			}))

			// content_block_start
			fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":          "content_block_start",
				"index":         0,
				"content_block": map[string]interface{}{"type": "text", "text": ""},
			}))

			// content_block_delta
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]interface{}{"type": "text_delta", "text": fmt.Sprintf("response turn %d", turn)},
			}))

			// content_block_stop
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "content_block_stop", "index": 0,
			}))

			// message_delta with final usage
			fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": "end_turn"},
				"usage": map[string]interface{}{"output_tokens": completionTokens},
			}))

			// message_stop
			fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", mustJSONStr(map[string]interface{}{
				"type": "message_stop",
			}))
		} else {
			resp := map[string]interface{}{
				"id":          fmt.Sprintf("msg_%d", turn),
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": fmt.Sprintf("response turn %d", turn)}},
				"stop_reason": "end_turn",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens":  cacheWrite,
					"cache_read_input_tokens":      cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

// --- Anthropic mock server with tool_use response ---

func newAnthropicToolMockServer(t *testing.T, cache *cachedPrefix) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		turn++
		messages := req["messages"].([]interface{})
		promptTokens := len(messages) * 50
		cacheRead, cacheWrite := cache.hit(promptTokens)
		completionTokens := 15

		// Verify tools have cache_control on last
		if toolsVal, ok := req["tools"].([]interface{}); ok {
			for i, tv := range toolsVal {
				tool := tv.(map[string]interface{})
				_, cc := tool["cache_control"]
				isLast := i == len(toolsVal)-1
				if cc && !isLast {
					t.Errorf("[turn %d] tool[%d] has cache_control but is not last", turn, i)
				}
				if !cc && isLast {
					t.Errorf("[turn %d] last tool missing cache_control", turn)
				}
			}
		}

		t.Logf("[Anthropic-tool turn %d] messages=%d cache_read=%d cache_write=%d", turn, len(messages), cacheRead, cacheWrite)

		if turn == 1 {
			// Return tool_use
			resp := map[string]interface{}{
				"id":   "msg_tool",
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_use", "id": "call_abc", "name": "read", "input": map[string]interface{}{"path": "test.go"}},
				},
				"stop_reason": "tool_use",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens":  cacheWrite,
					"cache_read_input_tokens":      cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		} else {
			// Return text
			resp := map[string]interface{}{
				"id":          "msg_final",
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": "done with tool"}},
				"stop_reason": "end_turn",
				"usage": map[string]interface{}{
					"input_tokens":                promptTokens,
					"output_tokens":               completionTokens,
					"cache_creation_input_tokens":  cacheWrite,
					"cache_read_input_tokens":      cacheRead,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
}

func mustJSONStr(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// =============================================================================
// Test scenarios
// =============================================================================

func TestOpenAIProtocol_MultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runMultiTurnScenario(t, prov, "OpenAI non-stream")
}

func TestOpenAIProtocol_StreamingMultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runStreamingMultiTurnScenario(t, prov, "OpenAI stream")
}

func TestAnthropicProtocol_MultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runMultiTurnScenario(t, prov, "Anthropic non-stream")
}

func TestAnthropicProtocol_StreamingMultiTurnCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runStreamingMultiTurnScenario(t, prov, "Anthropic stream")
}

func TestAnthropicProtocol_ToolCallCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicToolMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})

	ctx := testContext()
	sys := NewTextMessage("system", "You are a tool-using assistant.")
	user1 := NewTextMessage("user", "Read test.go")
	tools := []ToolDefinition{
		{Type: "function", Function: FunctionDefinition{Name: "read", Description: "read file",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}}}},
		{Type: "function", Function: FunctionDefinition{Name: "write", Description: "write file"}},
	}

	// Turn 1: triggers tool_use
	req1 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1},
		Tools: tools, CacheRetention: CacheShort, SessionID: "sess-tool",
	}
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	assertCacheFields(t, "tool turn 1", resp1.Usage)

	// Turn 2: tool_result + follow-up (simulates the agent loop)
	assistant1 := resp1.Choices[0].Message
	toolResult := NewToolResultMessage("call_abc", "package main...")
	user2 := NewTextMessage("user", "What does it do?")

	req2 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1, assistant1, toolResult, user2},
		Tools: tools, CacheRetention: CacheShort, SessionID: "sess-tool",
	}
	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	assertCacheFields(t, "tool turn 2", resp2.Usage)

	if resp2.Usage.CacheReadTokens == 0 {
		t.Error("tool turn 2: expected cache_read > 0")
	}

	t.Logf("\n=== Tool Call Cache Summary ===")
	logTurn(t, 1, resp1.Usage)
	logTurn(t, 2, resp2.Usage)
}

func TestOpenAIProtocol_SubagentForkCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newOpenAIMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewOpenAIProvider(&ProviderConfig{
		Provider: "openai", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runForkScenario(t, prov, "OpenAI fork")
}

func TestAnthropicProtocol_SubagentForkCache(t *testing.T) {
	cache := &cachedPrefix{}
	srv := newAnthropicMockServer(t, cache)
	defer srv.Close()

	prov, _ := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: srv.URL, APIKey: "test", Timeout: 10,
	})
	runForkScenario(t, prov, "Anthropic fork")
}

// --- Live test against DeepSeek (both protocols) ---

func TestLive_OpenAIProtocol_AllScenarios(t *testing.T) {
	cfg, prov := skipLive(t)
	_ = cfg
	t.Run("MultiTurn", func(t *testing.T) { runMultiTurnScenario(t, prov, "Live-OpenAI") })
	t.Run("Streaming", func(t *testing.T) { runStreamingMultiTurnScenario(t, prov, "Live-OpenAI-Stream") })
	t.Run("Fork", func(t *testing.T) { runForkScenario(t, prov, "Live-OpenAI-Fork") })
}

// =============================================================================
// Shared scenario runners
// =============================================================================

func runMultiTurnScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	ctx := testContext()
	sys := NewTextMessage("system", "You are a helpful assistant. "+strings.Repeat("You have deep expertise in mathematics and always answer with just the numeric result. ", 30))
	user1 := NewTextMessage("user", "What is 2+2?")

	// Turn 1
	req1 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("%s turn 1: %v", label, err)
	}
	assertCacheFields(t, label+" turn 1", resp1.Usage)

	// Turn 2
	a1 := resp1.Choices[0].Message
	user2 := NewTextMessage("user", "What is 3+3?")
	req2 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1, a1, user2},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("%s turn 2: %v", label, err)
	}
	assertCacheFields(t, label+" turn 2", resp2.Usage)

	// Turn 3
	a2 := resp2.Choices[0].Message
	user3 := NewTextMessage("user", "What is 4+4?")
	req3 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1, a1, user2, a2, user3},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-mt",
	}
	resp3, err := prov.ChatCompletion(ctx, req3)
	if err != nil {
		t.Fatalf("%s turn 3: %v", label, err)
	}
	assertCacheFields(t, label+" turn 3", resp3.Usage)

	// Context should grow
	if resp3.Usage.PromptTokens <= resp1.Usage.PromptTokens {
		t.Errorf("%s: prompt tokens should grow (turn1=%d turn3=%d)",
			label, resp1.Usage.PromptTokens, resp3.Usage.PromptTokens)
	}

	// Cache should improve (may be 0 if prompt is below provider's minimum cache threshold)
	if resp2.Usage.CacheReadTokens == 0 && resp3.Usage.CacheReadTokens == 0 {
		t.Logf("%s: WARNING cache_read=0 in turn 2 and 3 — prompt may be below provider minimum cache threshold", label)
	}

	t.Logf("\n=== %s Multi-Turn Summary ===", label)
	logTurn(t, 1, resp1.Usage)
	logTurn(t, 2, resp2.Usage)
	logTurn(t, 3, resp3.Usage)
}

func runStreamingMultiTurnScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	sp, ok := prov.(StreamingProvider)
	if !ok {
		t.Skipf("%s: provider does not support streaming", label)
	}
	ctx := testContext()
	sys := NewTextMessage("system", "You translate to French. "+strings.Repeat("Always respond with just the translation. ", 30))

	// Turn 1
	user1 := NewTextMessage("user", "Hello")
	req1 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-stream", Stream: true,
	}
	msg1, usage1 := collectStream(t, sp, ctx, req1)

	// Turn 2
	a1 := NewTextMessage("assistant", msg1)
	user2 := NewTextMessage("user", "Goodbye")
	req2 := &ChatCompletionRequest{
		Messages: []ChatMessage{sys, user1, a1, user2},
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-stream", Stream: true,
	}
	_, usage2 := collectStream(t, sp, ctx, req2)

	if usage1 == nil || usage2 == nil {
		t.Fatalf("%s: streaming did not return usage", label)
	}

	if usage2.CacheReadTokens == 0 {
		t.Errorf("%s: expected cache_read > 0 in stream turn 2", label)
	}

	t.Logf("\n=== %s Streaming Summary ===", label)
	logTurn(t, 1, usage1)
	logTurn(t, 2, usage2)
}

func runForkScenario(t *testing.T, prov Provider, label string) {
	t.Helper()
	ctx := testContext()
	sys := NewTextMessage("system", "You are a scanner. "+strings.Repeat("Analyze targets. ", 30))

	// Build parent conversation (3 exchanges)
	parentMsgs := []ChatMessage{sys}
	for i := 1; i <= 3; i++ {
		parentMsgs = append(parentMsgs,
			NewTextMessage("user", fmt.Sprintf("question %d", i)),
			NewTextMessage("assistant", fmt.Sprintf("answer %d", i)),
		)
	}

	// Parent's next request
	parentReq := &ChatCompletionRequest{
		Messages: append(append([]ChatMessage(nil), parentMsgs...), NewTextMessage("user", "parent question 4")),
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-fork",
	}
	parentResp, err := prov.ChatCompletion(ctx, parentReq)
	if err != nil {
		t.Fatalf("%s parent: %v", label, err)
	}

	// Fork child: inherits parent messages, new prompt
	childReq := &ChatCompletionRequest{
		Messages: append(append([]ChatMessage(nil), parentMsgs...), NewTextMessage("user", "forked child task")),
		MaxTokens: 50, CacheRetention: CacheShort, SessionID: "sess-fork",
	}
	childResp, err := prov.ChatCompletion(ctx, childReq)
	if err != nil {
		t.Fatalf("%s child: %v", label, err)
	}

	// Both should have cache reads (shared prefix)
	if childResp.Usage.CacheReadTokens == 0 {
		t.Errorf("%s: fork child expected cache_read > 0", label)
	}

	t.Logf("\n=== %s Fork Summary ===", label)
	t.Logf("  Parent: prompt=%d cache_read=%d cache_write=%d (%.0f%%)",
		parentResp.Usage.PromptTokens, parentResp.Usage.CacheReadTokens, parentResp.Usage.CacheWriteTokens,
		parentResp.Usage.CacheHitRatio()*100)
	t.Logf("  Child:  prompt=%d cache_read=%d cache_write=%d (%.0f%%)",
		childResp.Usage.PromptTokens, childResp.Usage.CacheReadTokens, childResp.Usage.CacheWriteTokens,
		childResp.Usage.CacheHitRatio()*100)
}

// --- helpers ---

func testContext() context.Context { return context.Background() }

func skipLive(t *testing.T) (*ProviderConfig, Provider) {
	t.Helper()
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL")
	}
	cfg := &ProviderConfig{BaseURL: baseURL, APIKey: apiKey, Model: model, Timeout: 60}
	cfg, _ = Resolve(cfg)
	p, _ := NewProviderFromResolved(cfg)
	return cfg, p
}

func collectStream(t *testing.T, sp StreamingProvider, ctx context.Context, req *ChatCompletionRequest) (string, *Usage) {
	t.Helper()
	ch, err := sp.ChatCompletionStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	var lastUsage *Usage
	for event := range ch {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Usage != nil {
			lastUsage = event.Usage
		}
		if event.Delta.Content != nil {
			content.WriteString(*event.Delta.Content)
		}
		if event.Done {
			break
		}
	}
	return content.String(), lastUsage
}

func assertCacheFields(t *testing.T, label string, u *Usage) {
	t.Helper()
	if u == nil {
		t.Errorf("%s: usage is nil", label)
		return
	}
	if u.PromptTokens == 0 {
		t.Errorf("%s: prompt_tokens = 0", label)
	}
	// Cache fields should be non-negative (0 is fine for first turn)
	if u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 {
		t.Errorf("%s: negative cache tokens: read=%d write=%d", label, u.CacheReadTokens, u.CacheWriteTokens)
	}
}

func logTurn(t *testing.T, turn int, u *Usage) {
	t.Helper()
	if u == nil {
		t.Logf("  Turn %d: usage=nil", turn)
		return
	}
	t.Logf("  Turn %d: prompt=%d completion=%d cache_read=%d cache_write=%d hit_ratio=%.0f%%",
		turn, u.PromptTokens, u.CompletionTokens,
		u.CacheReadTokens, u.CacheWriteTokens, u.CacheHitRatio()*100)
}
