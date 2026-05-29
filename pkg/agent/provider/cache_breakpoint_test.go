package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestCacheBreakpointPlacementMultiTurn verifies that cache_control markers
// are placed correctly as the conversation grows across turns, and that the
// prefix is never broken.
func TestCacheBreakpointPlacementMultiTurn(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := NewTextMessage("system", "You are a helpful assistant.")
	tools := []ToolDefinition{
		{Type: "function", Function: FunctionDefinition{Name: "read", Description: "read file"}},
		{Type: "function", Function: FunctionDefinition{Name: "write", Description: "write file"}},
	}

	// --- Turn 1: system + user1 ---
	user1 := NewTextMessage("user", "Hello turn 1")
	req1 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []ChatMessage{sysMsg, user1},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j1 := mustMarshal(t, prov, req1)
	p1 := mustParse(t, j1)

	t.Log("=== Turn 1 ===")
	assertSystemCached(t, p1)
	assertLastToolCached(t, p1)
	assertLastUserCached(t, p1)
	assertNoCacheOnRole(t, p1, "assistant")
	t.Log(prettyJSON(p1))

	// --- Turn 2: system + user1 + assistant1 + user2 ---
	assistant1 := NewTextMessage("assistant", "Hi there")
	user2 := NewTextMessage("user", "Hello turn 2")
	req2 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []ChatMessage{sysMsg, user1, assistant1, user2},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j2 := mustMarshal(t, prov, req2)
	p2 := mustParse(t, j2)

	t.Log("=== Turn 2 ===")
	assertSystemCached(t, p2)
	assertLastToolCached(t, p2)

	msgs2 := p2["messages"].([]interface{})
	// user1 should NOT have cache_control (it's no longer the last user msg)
	firstUserBlocks := msgs2[0].(map[string]interface{})["content"].([]interface{})
	firstUserLastBlock := firstUserBlocks[len(firstUserBlocks)-1].(map[string]interface{})
	if _, ok := firstUserLastBlock["cache_control"]; ok {
		t.Error("Turn 2: user1 should NOT have cache_control (only last user msg gets it)")
	}

	// user2 (last user msg) SHOULD have cache_control
	lastUserMsg := findLastMsgWithRole(msgs2, "user")
	lastUserBlocks := lastUserMsg["content"].([]interface{})
	lastBlock := lastUserBlocks[len(lastUserBlocks)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Error("Turn 2: user2 (last user) should have cache_control")
	}

	// Verify prefix stability: system and tools are identical between turn 1 and turn 2
	assertPrefixStable(t, "system", p1, p2)
	assertPrefixStable(t, "tools", p1, p2)

	// --- Turn 3: with tool_result (maps to user role) ---
	tc := ToolCall{ID: "call_1", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{"path":"test.go"}`}}
	assistant2 := ChatMessage{Role: "assistant", ToolCalls: []ToolCall{tc}}
	toolResult := NewToolResultMessage("call_1", "file contents here")
	user3 := NewTextMessage("user", "Now what?")

	req3 := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       []ChatMessage{sysMsg, user1, assistant1, user2, assistant2, toolResult, user3},
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	j3 := mustMarshal(t, prov, req3)
	p3 := mustParse(t, j3)

	t.Log("=== Turn 3 (with tool_result) ===")
	assertSystemCached(t, p3)
	assertLastToolCached(t, p3)

	// After mergeConsecutive, tool_result (user role) and user3 (user role) may merge.
	// The cache_control should be on the LAST content block of the LAST user message.
	msgs3 := p3["messages"].([]interface{})
	lastUser3 := findLastMsgWithRole(msgs3, "user")
	blocks3 := lastUser3["content"].([]interface{})
	finalBlock := blocks3[len(blocks3)-1].(map[string]interface{})
	if _, ok := finalBlock["cache_control"]; !ok {
		t.Error("Turn 3: last content block of last user message should have cache_control")
	}

	// Earlier user messages should NOT have cache_control
	for i := 0; i < len(msgs3)-1; i++ {
		msg := msgs3[i].(map[string]interface{})
		if msg["role"] != "user" {
			continue
		}
		blocks := msg["content"].([]interface{})
		for _, b := range blocks {
			block := b.(map[string]interface{})
			if _, ok := block["cache_control"]; ok {
				t.Errorf("Turn 3: non-last user message at index %d should NOT have cache_control", i)
			}
		}
	}
}

// TestCacheBreakpointSubagentFork simulates what happens in fork mode:
// child inherits parent messages + adds its own prompt.
func TestCacheBreakpointSubagentFork(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := NewTextMessage("system", "You are a security scanner.")
	tools := []ToolDefinition{
		{Type: "function", Function: FunctionDefinition{Name: "scan", Description: "scan target"}},
	}

	// Parent conversation: system + user1 + assistant1 + user2 + assistant2
	user1 := NewTextMessage("user", "Scan target.com")
	assistant1 := NewTextMessage("assistant", "Starting scan...")
	user2 := NewTextMessage("user", "Check port 443")
	assistant2 := NewTextMessage("assistant", "Port 443 is open")

	parentMessages := []ChatMessage{user1, assistant1, user2, assistant2}

	// Fork child: inherits parent messages, adds child prompt as new user message
	childPrompt := NewTextMessage("user", "Analyze the SSL certificate on port 443")
	childMessages := append([]ChatMessage{sysMsg}, parentMessages...)
	childMessages = append(childMessages, childPrompt)

	// Parent's last request (before forking)
	parentReq := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       append([]ChatMessage{sysMsg}, append(parentMessages, NewTextMessage("user", "fork a subagent"))...),
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	parentJSON := mustMarshal(t, prov, parentReq)
	parentParsed := mustParse(t, parentJSON)

	// Child's request
	childReq := &ChatCompletionRequest{
		Model:          "claude-sonnet-4-20250514",
		Messages:       childMessages,
		Tools:          tools,
		CacheRetention: CacheShort,
	}
	childJSON := mustMarshal(t, prov, childReq)
	childParsed := mustParse(t, childJSON)

	t.Log("=== Parent Request ===")
	t.Log(prettyJSON(parentParsed))
	t.Log("=== Child (Fork) Request ===")
	t.Log(prettyJSON(childParsed))

	// System and tools should be identical (cache-friendly prefix)
	assertPrefixStable(t, "system", parentParsed, childParsed)
	assertPrefixStable(t, "tools", parentParsed, childParsed)

	// Both should have system and tools cached
	assertSystemCached(t, parentParsed)
	assertSystemCached(t, childParsed)
	assertLastToolCached(t, parentParsed)
	assertLastToolCached(t, childParsed)

	// Child's shared prefix (system + tools + parent messages) should match parent's prefix
	parentMsgs := parentParsed["messages"].([]interface{})
	childMsgs := childParsed["messages"].([]interface{})

	// The shared prefix is all messages except the last user message in each.
	// Parent has: user1, assistant1, user2, assistant2, user("fork a subagent")
	// Child has:  user1, assistant1, user2, assistant2, user("Analyze the SSL...")
	// Shared prefix: user1, assistant1, user2, assistant2 — these 4 should be identical.
	sharedLen := min(len(parentMsgs), len(childMsgs)) - 1
	for i := 0; i < sharedLen; i++ {
		pMsg := stripCacheControl(parentMsgs[i])
		cMsg := stripCacheControl(childMsgs[i])
		pJSON, _ := json.Marshal(pMsg)
		cJSON, _ := json.Marshal(cMsg)
		if string(pJSON) != string(cJSON) {
			t.Errorf("Shared prefix diverges at message %d:\n  parent: %s\n  child:  %s", i, pJSON, cJSON)
		}
	}
	t.Logf("Shared prefix verified: %d messages identical (excluding cache_control)", sharedLen)

	// Only the LAST user message in each should have cache_control
	parentLastUser := findLastMsgWithRole(parentMsgs, "user")
	childLastUser := findLastMsgWithRole(childMsgs, "user")
	assertBlockHasCacheControl(t, "parent last user", parentLastUser)
	assertBlockHasCacheControl(t, "child last user", childLastUser)
}

// TestCacheNoneProducesNoCacheControl verifies CacheNone never adds markers.
func TestCacheNoneProducesNoCacheControl(t *testing.T) {
	prov := mustAnthropicProvider(t)

	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []ChatMessage{
			NewTextMessage("system", "system prompt"),
			NewTextMessage("user", "hello"),
			NewTextMessage("assistant", "hi"),
			NewTextMessage("user", "bye"),
		},
		Tools: []ToolDefinition{
			{Type: "function", Function: FunctionDefinition{Name: "tool1", Description: "t1"}},
		},
		CacheRetention: CacheNone,
	}
	data := mustMarshal(t, prov, req)
	if strings.Contains(string(data), "cache_control") {
		t.Error("CacheNone request contains cache_control")
		t.Log(string(data))
	}
}

// --- helpers ---

func mustAnthropicProvider(t *testing.T) *AnthropicProvider {
	t.Helper()
	p, err := NewAnthropicProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: "https://api.anthropic.com/v1",
		APIKey: "test", Timeout: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustMarshal(t *testing.T, p *AnthropicProvider, req *ChatCompletionRequest) []byte {
	t.Helper()
	data, err := p.marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustParse(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func prettyJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func assertSystemCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	sys, ok := parsed["system"].([]interface{})
	if !ok {
		t.Error("system should be array when cache enabled")
		return
	}
	block := sys[len(sys)-1].(map[string]interface{})
	if _, ok := block["cache_control"]; !ok {
		t.Error("system prompt should have cache_control")
	}
}

func assertLastToolCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	tools, ok := parsed["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		return // no tools to check
	}
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, ok := lastTool["cache_control"]; !ok {
		t.Error("last tool should have cache_control")
	}
	// Ensure non-last tools do NOT have cache_control
	for i := 0; i < len(tools)-1; i++ {
		tool := tools[i].(map[string]interface{})
		if _, ok := tool["cache_control"]; ok {
			t.Errorf("tool[%d] should NOT have cache_control (only last tool)", i)
		}
	}
}

func assertLastUserCached(t *testing.T, parsed map[string]interface{}) {
	t.Helper()
	msgs := parsed["messages"].([]interface{})
	lastUser := findLastMsgWithRole(msgs, "user")
	if lastUser == nil {
		t.Error("no user message found")
		return
	}
	assertBlockHasCacheControl(t, "last user", lastUser)
}

func assertNoCacheOnRole(t *testing.T, parsed map[string]interface{}, role string) {
	t.Helper()
	msgs := parsed["messages"].([]interface{})
	for i, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["role"] != role {
			continue
		}
		blocks, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, b := range blocks {
			block := b.(map[string]interface{})
			if _, ok := block["cache_control"]; ok {
				t.Errorf("message[%d] (role=%s) should NOT have cache_control", i, role)
			}
		}
	}
}

func assertBlockHasCacheControl(t *testing.T, label string, msg map[string]interface{}) {
	t.Helper()
	blocks, ok := msg["content"].([]interface{})
	if !ok || len(blocks) == 0 {
		t.Errorf("%s: no content blocks", label)
		return
	}
	lastBlock := blocks[len(blocks)-1].(map[string]interface{})
	if _, ok := lastBlock["cache_control"]; !ok {
		t.Errorf("%s: last content block should have cache_control", label)
	}
}

func assertPrefixStable(t *testing.T, key string, a, b map[string]interface{}) {
	t.Helper()
	aJSON, _ := json.Marshal(a[key])
	bJSON, _ := json.Marshal(b[key])
	if string(aJSON) != string(bJSON) {
		t.Errorf("%s prefix differs between requests:\n  a: %s\n  b: %s", key, aJSON, bJSON)
	}
}

func findLastMsgWithRole(msgs []interface{}, role string) map[string]interface{} {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i].(map[string]interface{})
		if msg["role"] == role {
			return msg
		}
	}
	return nil
}

func stripCacheControl(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := make(map[string]interface{})
	for k, val := range m {
		if k == "cache_control" {
			continue
		}
		switch typed := val.(type) {
		case []interface{}:
			stripped := make([]interface{}, len(typed))
			for i, item := range typed {
				stripped[i] = stripCacheControl(item)
			}
			out[k] = stripped
		case map[string]interface{}:
			out[k] = stripCacheControl(typed)
		default:
			out[k] = val
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestCacheBreakpointToolResultMerge verifies that when tool_result (user role)
// is the last message and gets merged with preceding user messages,
// cache_control lands on the correct final block.
func TestCacheBreakpointToolResultMerge(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sysMsg := NewTextMessage("system", "system prompt")
	user1 := NewTextMessage("user", "call the tool")
	tc := ToolCall{ID: "c1", Type: "function", Function: FunctionCall{Name: "read", Arguments: `{}`}}
	assistant1 := ChatMessage{Role: "assistant", ToolCalls: []ToolCall{tc}}
	toolResult := NewToolResultMessage("c1", "file content here")

	// Case A: tool_result is the LAST message (no user msg after it)
	// tool_result maps to user role → it becomes the "last user message"
	reqA := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []ChatMessage{sysMsg, user1, assistant1, toolResult},
		CacheRetention: CacheShort,
	}
	jA := mustMarshal(t, prov, reqA)
	pA := mustParse(t, jA)
	msgsA := pA["messages"].([]interface{})

	t.Log("=== Case A: tool_result is last ===")
	lastUserA := findLastMsgWithRole(msgsA, "user")
	if lastUserA == nil {
		t.Fatal("no user message found")
	}
	blocksA := lastUserA["content"].([]interface{})
	lastBlockA := blocksA[len(blocksA)-1].(map[string]interface{})
	if _, ok := lastBlockA["cache_control"]; !ok {
		t.Error("Case A: tool_result block (last user msg) should have cache_control")
	}
	t.Logf("  Last user msg has %d blocks, cache_control on last: type=%v",
		len(blocksA), lastBlockA["type"])

	// Case B: tool_result followed by user message → they merge (consecutive user role)
	user2 := NewTextMessage("user", "now analyze it")
	reqB := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []ChatMessage{sysMsg, user1, assistant1, toolResult, user2},
		CacheRetention: CacheShort,
	}
	jB := mustMarshal(t, prov, reqB)
	pB := mustParse(t, jB)
	msgsB := pB["messages"].([]interface{})

	t.Log("=== Case B: tool_result + user merge ===")
	lastUserB := findLastMsgWithRole(msgsB, "user")
	blocksB := lastUserB["content"].([]interface{})
	lastBlockB := blocksB[len(blocksB)-1].(map[string]interface{})
	if _, ok := lastBlockB["cache_control"]; !ok {
		t.Error("Case B: merged user msg's last block should have cache_control")
	}
	t.Logf("  Merged user msg has %d blocks, cache_control on last: type=%v text=%v",
		len(blocksB), lastBlockB["type"], lastBlockB["text"])

	// Case C: multiple tool calls → multiple tool_results merge into one user message
	tc2 := ToolCall{ID: "c2", Type: "function", Function: FunctionCall{Name: "write", Arguments: `{}`}}
	assistant2 := ChatMessage{Role: "assistant", ToolCalls: []ToolCall{tc, tc2}}
	toolResult1 := NewToolResultMessage("c1", "result1")
	toolResult2 := NewToolResultMessage("c2", "result2")

	reqC := &ChatCompletionRequest{
		Model:          "test",
		Messages:       []ChatMessage{sysMsg, user1, assistant2, toolResult1, toolResult2},
		CacheRetention: CacheShort,
	}
	jC := mustMarshal(t, prov, reqC)
	pC := mustParse(t, jC)
	msgsC := pC["messages"].([]interface{})

	t.Log("=== Case C: multiple tool_results merge ===")
	lastUserC := findLastMsgWithRole(msgsC, "user")
	blocksC := lastUserC["content"].([]interface{})
	lastBlockC := blocksC[len(blocksC)-1].(map[string]interface{})
	if _, ok := lastBlockC["cache_control"]; !ok {
		t.Error("Case C: merged tool_results' last block should have cache_control")
	}
	t.Logf("  Merged tool_results has %d blocks, cache_control on last: type=%v",
		len(blocksC), lastBlockC["type"])
	for i, b := range blocksC {
		block := b.(map[string]interface{})
		_, cc := block["cache_control"]
		t.Logf("    block[%d] type=%v cache_control=%v", i, block["type"], cc)
	}
}

// TestCacheBreakpointStabilityAcrossTurns checks that the shared prefix bytes
// between consecutive turns are byte-identical (excluding the moving cache_control).
func TestCacheBreakpointStabilityAcrossTurns(t *testing.T) {
	prov := mustAnthropicProvider(t)

	sys := NewTextMessage("system", "system prompt here")
	tools := []ToolDefinition{
		{Type: "function", Function: FunctionDefinition{Name: "tool1", Description: "desc"}},
	}

	// Build 5 turns of conversation
	msgs := []ChatMessage{sys}
	for turn := 1; turn <= 5; turn++ {
		msgs = append(msgs, NewTextMessage("user", fmt.Sprintf("question %d", turn)))
		msgs = append(msgs, NewTextMessage("assistant", fmt.Sprintf("answer %d", turn)))
	}
	msgs = append(msgs, NewTextMessage("user", "final question"))

	// Marshal the full request
	reqFull := &ChatCompletionRequest{
		Model: "test", Messages: msgs, Tools: tools, CacheRetention: CacheShort,
	}
	jFull := mustMarshal(t, prov, reqFull)
	pFull := mustParse(t, jFull)

	// Marshal a shorter prefix (first 3 turns + new question)
	shortMsgs := append(msgs[:7], NewTextMessage("user", "different question")) // sys + 3 turns + new user
	reqShort := &ChatCompletionRequest{
		Model: "test", Messages: shortMsgs, Tools: tools, CacheRetention: CacheShort,
	}
	jShort := mustMarshal(t, prov, reqShort)
	pShort := mustParse(t, jShort)

	// System and tools MUST be identical
	assertPrefixStable(t, "system", pFull, pShort)
	assertPrefixStable(t, "tools", pFull, pShort)

	// Shared message prefix (first 6 messages: 3 user+assistant pairs) should be identical
	fullMsgs := pFull["messages"].([]interface{})
	shortMsgs2 := pShort["messages"].([]interface{})

	sharedCount := len(shortMsgs2) - 1 // exclude last (which has cache_control)
	for i := 0; i < sharedCount; i++ {
		fJSON, _ := json.Marshal(stripCacheControl(fullMsgs[i]))
		sJSON, _ := json.Marshal(stripCacheControl(shortMsgs2[i]))
		if string(fJSON) != string(sJSON) {
			t.Errorf("shared message[%d] differs", i)
		}
	}
	t.Logf("Prefix stable across turns: %d shared messages verified", sharedCount)
}
