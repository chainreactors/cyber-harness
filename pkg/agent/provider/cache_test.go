package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLiveCacheMetrics(t *testing.T) {
	apiKey := os.Getenv("TEST_API_KEY")
	baseURL := os.Getenv("TEST_BASE_URL")
	model := os.Getenv("TEST_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("set TEST_API_KEY, TEST_BASE_URL, TEST_MODEL to run live cache test")
	}

	cfg := &ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 60,
	}
	cfg, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := NewProviderFromResolved(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Build a substantial system prompt to exceed provider's minimum cache threshold
	systemPrompt := "You are a helpful security analysis assistant. " + strings.Repeat("You have deep expertise in vulnerability assessment, penetration testing, and secure code review. ", 40)

	sysMsg := NewTextMessage("system", systemPrompt)
	userMsg1 := NewTextMessage("user", "What is 2+2? Answer in one word.")

	// Turn 1
	req1 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []ChatMessage{sysMsg, userMsg1},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	ctx := context.Background()
	resp1, err := prov.ChatCompletion(ctx, req1)
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}

	t.Logf("=== Turn 1 ===")
	t.Logf("Response: %s", deref(resp1.Choices[0].Message.Content))
	logUsage(t, resp1.Usage)

	// Turn 2 — same prefix, new user message
	assistantReply := resp1.Choices[0].Message
	userMsg2 := NewTextMessage("user", "What is 3+3? Answer in one word.")

	req2 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []ChatMessage{sysMsg, userMsg1, assistantReply, userMsg2},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	resp2, err := prov.ChatCompletion(ctx, req2)
	if err != nil {
		t.Fatalf("turn 2 failed: %v", err)
	}

	t.Logf("=== Turn 2 ===")
	t.Logf("Response: %s", deref(resp2.Choices[0].Message.Content))
	logUsage(t, resp2.Usage)

	// Turn 3 — even longer prefix
	assistantReply2 := resp2.Choices[0].Message
	userMsg3 := NewTextMessage("user", "What is 4+4? Answer in one word.")

	req3 := &ChatCompletionRequest{
		Model:          model,
		Messages:       []ChatMessage{sysMsg, userMsg1, assistantReply, userMsg2, assistantReply2, userMsg3},
		MaxTokens:      50,
		CacheRetention: CacheShort,
		SessionID:      "test-cache-session-001",
	}

	resp3, err := prov.ChatCompletion(ctx, req3)
	if err != nil {
		t.Fatalf("turn 3 failed: %v", err)
	}

	t.Logf("=== Turn 3 ===")
	t.Logf("Response: %s", deref(resp3.Choices[0].Message.Content))
	logUsage(t, resp3.Usage)

	// Summary
	t.Logf("\n=== Cache Summary ===")
	for i, resp := range []*ChatCompletionResponse{resp1, resp2, resp3} {
		if resp.Usage != nil {
			ratio := 0.0
			if resp.Usage.PromptTokens > 0 {
				ratio = float64(resp.Usage.CacheReadTokens) / float64(resp.Usage.PromptTokens) * 100
			}
			t.Logf("Turn %d: prompt=%d cache_read=%d cache_write=%d hit_ratio=%.1f%%",
				i+1, resp.Usage.PromptTokens, resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens, ratio)
		}
	}
}

func logUsage(t *testing.T, u *Usage) {
	if u == nil {
		t.Log("Usage: nil")
		return
	}
	raw, _ := json.Marshal(u)
	t.Logf("Usage: %s", raw)
	t.Logf("  prompt=%d completion=%d total=%d cache_read=%d cache_write=%d",
		u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CacheReadTokens, u.CacheWriteTokens)
}

// Also test that the marshalRequest correctly adds cache_control for Anthropic
func TestAnthropicMarshalCacheControl(t *testing.T) {
	cfg := &ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://api.anthropic.com/v1",
		APIKey:   "test-key",
		Timeout:  60,
	}
	prov, err := NewAnthropicProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sysMsg := NewTextMessage("system", "You are a helpful assistant.")
	userMsg := NewTextMessage("user", "Hello")

	// Without cache
	req := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []ChatMessage{sysMsg, userMsg},
		CacheRetention: CacheNone,
	}
	data, err := prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cache_control") {
		t.Error("CacheNone should NOT include cache_control")
	}

	// With cache
	req.CacheRetention = CacheShort
	data, err = prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	// Check system prompt has cache_control
	sys, ok := parsed["system"].([]interface{})
	if !ok {
		t.Fatalf("system should be array when cache enabled, got %T", parsed["system"])
	}
	sysBlock := sys[0].(map[string]interface{})
	if _, ok := sysBlock["cache_control"]; !ok {
		t.Error("system prompt block should have cache_control")
	}

	// Check last user message has cache_control
	msgs := parsed["messages"].([]interface{})
	lastMsg := msgs[len(msgs)-1].(map[string]interface{})
	content := lastMsg["content"].([]interface{})
	lastBlock := content[len(content)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Error("last user message block should have cache_control")
	}

	t.Logf("Marshaled JSON (cache enabled):\n%s", string(data))
}

func TestAnthropicMarshalCacheControlWithTools(t *testing.T) {
	cfg := &ProviderConfig{
		Provider: "anthropic",
		BaseURL:  "https://api.anthropic.com/v1",
		APIKey:   "test-key",
		Timeout:  60,
	}
	prov, err := NewAnthropicProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sysMsg := NewTextMessage("system", "You are a helpful assistant.")
	userMsg := NewTextMessage("user", "Hello")

	tools := []ToolDefinition{
		{Type: "function", Function: FunctionDefinition{Name: "tool_a", Description: "first tool"}},
		{Type: "function", Function: FunctionDefinition{Name: "tool_b", Description: "second tool"}},
	}

	req := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []ChatMessage{sysMsg, userMsg},
		Tools:          tools,
		CacheRetention: CacheShort,
	}

	data, err := prov.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	// Check last tool has cache_control, first does not
	toolList := parsed["tools"].([]interface{})
	firstTool := toolList[0].(map[string]interface{})
	lastTool := toolList[len(toolList)-1].(map[string]interface{})

	if _, ok := firstTool["cache_control"]; ok {
		t.Error("first tool should NOT have cache_control")
	}
	if _, ok := lastTool["cache_control"]; !ok {
		t.Error("last tool should have cache_control")
	}

	t.Logf("Tools JSON: %s", mustJSON(toolList))
}

func TestOpenAIMarshalCacheKey(t *testing.T) {
	req := &ChatCompletionRequest{
		Model:          "gpt-4o",
		Messages:       []ChatMessage{NewTextMessage("user", "Hello")},
		CacheRetention: CacheShort,
		SessionID:      "sess-123",
	}

	data, err := marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed["prompt_cache_key"] != "sess-123" {
		t.Errorf("expected prompt_cache_key=sess-123, got %v", parsed["prompt_cache_key"])
	}
	if _, ok := parsed["prompt_cache_retention"]; ok {
		t.Error("CacheShort should NOT include prompt_cache_retention")
	}

	// CacheLong
	req.CacheRetention = CacheLong
	data, err = marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var parsedLong map[string]interface{}
	json.Unmarshal(data, &parsedLong)
	if parsedLong["prompt_cache_retention"] != "24h" {
		t.Errorf("CacheLong should set prompt_cache_retention=24h, got %v", parsedLong["prompt_cache_retention"])
	}

	// CacheNone
	req.CacheRetention = CacheNone
	data, err = marshalOpenAIRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var parsedNone map[string]interface{}
	json.Unmarshal(data, &parsedNone)
	if _, ok := parsedNone["prompt_cache_key"]; ok {
		t.Error("CacheNone should NOT include prompt_cache_key")
	}
}

func TestUsageUnmarshalDeepSeek(t *testing.T) {
	raw := `{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}`
	var u Usage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens: want 80, got %d", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 20 {
		t.Errorf("CacheWriteTokens: want 20, got %d", u.CacheWriteTokens)
	}
}

func TestUsageUnmarshalOpenAI(t *testing.T) {
	raw := `{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":10}}`
	var u Usage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.CacheReadTokens != 60 {
		t.Errorf("CacheReadTokens: want 60, got %d", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 10 {
		t.Errorf("CacheWriteTokens: want 10, got %d", u.CacheWriteTokens)
	}
}

func TestUsageUnmarshalNoCacheFields(t *testing.T) {
	raw := `{"prompt_tokens":50,"completion_tokens":10,"total_tokens":60}`
	var u Usage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 50 || u.CompletionTokens != 10 || u.TotalTokens != 60 {
		t.Errorf("basic fields wrong: %+v", u)
	}
	if u.CacheReadTokens != 0 || u.CacheWriteTokens != 0 {
		t.Error("cache tokens should be 0 when not present")
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func TestConvertAnthropicUsageCacheFields(t *testing.T) {
	u := convertAnthropicUsage(&anthropicUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 50,
		CacheReadInputTokens:     30,
	})
	if u.PromptTokens != 180 {
		t.Errorf("PromptTokens: want 180, got %d", u.PromptTokens)
	}
	if u.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens: want 30, got %d", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 50 {
		t.Errorf("CacheWriteTokens: want 50, got %d", u.CacheWriteTokens)
	}
	fmt.Println("usage:", mustJSON(u))
}
