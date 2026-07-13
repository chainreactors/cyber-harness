package agent

import (
	"testing"
)

func msg(role, content string) ChatMessage {
	return NewTextMessage(role, content)
}

func toolResult(id, content string) ChatMessage {
	return NewToolResultMessage(id, content)
}

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name string
		msg  ChatMessage
		want int
	}{
		{"empty", ChatMessage{Role: "user"}, 0},
		{"short text", msg("user", "hello"), 2},               // 5 chars → ceil(5/4) = 2
		{"exact boundary", msg("user", "abcd"), 1},             // 4 chars → 1
		{"longer text", msg("user", "hello world, this is a test message"), 9}, // 35 chars → ceil(35/4) = 9
		{"with tool calls", ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				Function: FunctionCall{Name: "bash", Arguments: `{"command":"ls -la"}`},
			}},
		}, 6}, // (4+19+3)/4 = 6
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateMessageTokens(tt.msg)
			if got != tt.want {
				t.Errorf("estimateMessageTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEstimateAllTokens(t *testing.T) {
	msgs := []ChatMessage{
		msg("user", "hello"),        // 2
		msg("assistant", "world"),   // 2
		msg("user", "how are you"), // 3
	}
	got := estimateAllTokens(msgs)
	want := 7
	if got != want {
		t.Errorf("estimateAllTokens() = %d, want %d", got, want)
	}
}

func TestFindCutPoint(t *testing.T) {
	longContent := make([]byte, 100000)
	for i := range longContent {
		longContent[i] = 'a'
	}
	longStr := string(longContent)

	tests := []struct {
		name       string
		msgs       []ChatMessage
		keepTokens int
		wantIdx    int
	}{
		{
			"all fit within budget",
			[]ChatMessage{msg("user", "hi"), msg("assistant", "hello")},
			20000,
			0,
		},
		{
			"cut at user message boundary",
			[]ChatMessage{
				msg("user", longStr),      // ~25000 tokens — old
				msg("assistant", longStr), // ~25000 tokens — old
				msg("user", "recent"),     // kept
				msg("assistant", "reply"), // kept
			},
			20000,
			2, // cut before the "recent" user message
		},
		{
			"skip tool results",
			[]ChatMessage{
				msg("user", longStr),
				msg("assistant", longStr),
				toolResult("tc1", "result"),
				msg("user", "recent"),
				msg("assistant", "reply"),
			},
			20000,
			3, // cut at the "recent" user message, skipping tool result
		},
		{
			"empty messages",
			[]ChatMessage{},
			20000,
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findCutPoint(tt.msgs, tt.keepTokens)
			if got != tt.wantIdx {
				t.Errorf("findCutPoint() = %d, want %d", got, tt.wantIdx)
			}
		})
	}
}

func TestSerializeMessages(t *testing.T) {
	msgs := []ChatMessage{
		msg("user", "search for bugs"),
		msg("assistant", "I'll search now"),
	}
	result := serializeMessages(msgs)
	if result == "" {
		t.Fatal("serializeMessages returned empty string")
	}
	if !contains(result, "[User]: search for bugs") {
		t.Errorf("missing user message in serialized output")
	}
	if !contains(result, "[Assistant]: I'll search now") {
		t.Errorf("missing assistant message in serialized output")
	}
}

func TestSerializeMessagesSkipsToolResultRoleUser(t *testing.T) {
	msgs := []ChatMessage{
		toolResult("tc1", "some tool output"),
	}
	result := serializeMessages(msgs)
	if contains(result, "[User]") {
		t.Error("tool result should not appear as User message")
	}
	if !contains(result, "[Tool Result]") {
		t.Error("tool result should appear as Tool Result")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
