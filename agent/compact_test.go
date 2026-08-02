package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func msg(role, content string) *aop.Message {
	return textMessage(role, content)
}

func toolResult(id, content string) *aop.Message {
	return toolResultMessage(id, content)
}

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name string
		msg  *aop.Message
		want int
	}{
		{"empty", &aop.Message{Role: "user"}, 0},
		{"short text", msg("user", "hello"), 2},                                // 5 chars → ceil(5/4) = 2
		{"exact boundary", msg("user", "abcd"), 1},                             // 4 chars → 1
		{"longer text", msg("user", "hello world, this is a test message"), 9}, // 35 chars → ceil(35/4) = 9
		{"image", imageMessage("user", aop.Image("image/png", []byte("data"))), 1200},
		{"with tool calls", &aop.Message{
			Role:    "assistant",
			Content: []*aop.Content{toolCallContent("", "bash", `{"command":"ls -la"}`)},
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
	msgs := []*aop.Message{
		msg("user", "hello"),       // 2
		msg("assistant", "world"),  // 2
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
		msgs       []*aop.Message
		keepTokens int
		wantIdx    int
	}{
		{
			"all fit within budget",
			[]*aop.Message{msg("user", "hi"), msg("assistant", "hello")},
			20000,
			0,
		},
		{
			"split a single oversized turn",
			[]*aop.Message{msg("user", longStr), msg("assistant", "recent")},
			20000,
			1,
		},
		{
			"split at assistant boundary to honor recent budget",
			[]*aop.Message{
				msg("user", longStr),      // ~25000 tokens — old
				msg("assistant", longStr), // ~25000 tokens — old
				msg("user", "recent"),     // kept
				msg("assistant", "reply"), // kept
			},
			20000,
			1, // Pi-style split turn keeps from the nearest valid assistant boundary
		},
		{
			"assistant boundary before old tool result",
			[]*aop.Message{
				msg("user", longStr),
				msg("assistant", longStr),
				toolResult("tc1", "result"),
				msg("user", "recent"),
				msg("assistant", "reply"),
			},
			20000,
			1, // assistant is valid; a tool result itself is never a cut point
		},
		{
			"split an oversized tool turn at an assistant boundary",
			[]*aop.Message{
				msg("user", longStr),
				msg("assistant", "calling a tool"),
				toolResult("tc1", longStr),
				msg("assistant", "tool follow-up"),
			},
			20000,
			3, // never retain from the tool result; summarize the turn prefix
		},
		{
			"empty messages",
			[]*aop.Message{},
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

func TestCompactHistorySummarizesOversizedTurnPrefix(t *testing.T) {
	long := strings.Repeat("x", 100000)
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "prefix checkpoint")),
	}}
	toolCall := ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{
		ID: "tc1", Type: "function", Function: FunctionCall{Name: "bash", Arguments: `{"command":"scan"}`},
	}}}.toAOP()
	messages := []*aop.Message{
		msg("user", long),
		toolCall,
		toolResult("tc1", long),
		msg("assistant", "recent suffix"),
	}

	compacted, result, err := compactHistory(context.Background(), CompactConfig{
		Provider: llm, Model: "custom", KeepRecentTokens: 20000,
		ReserveTokens: 16384, MaxTokens: 16384,
	}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.requestsSnapshot()) != 1 {
		t.Fatalf("summary requests = %d, want one turn-prefix request", len(llm.requestsSnapshot()))
	}
	if len(compacted) != 2 || compacted[1].Role != "assistant" || messageContent(compacted[1]) != "recent suffix" {
		t.Fatalf("compacted messages = %#v", compacted)
	}
	if !strings.Contains(messageContent(compacted[0]), "Turn Context (split turn)") ||
		!strings.Contains(messageContent(compacted[0]), "prefix checkpoint") {
		t.Fatalf("split-turn summary = %q", messageContent(compacted[0]))
	}
	if result.KeptMessages != 1 {
		t.Fatalf("compact result = %+v", result)
	}
}

func TestSerializeMessages(t *testing.T) {
	msgs := []*aop.Message{
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
	msgs := []*aop.Message{
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

func TestShouldCompactContextUsesReserveThreshold(t *testing.T) {
	settings := CompactionSettings{ReserveTokens: 16384}
	if shouldCompactContext(983616, 1000000, settings) {
		t.Fatal("equal-to threshold should not compact")
	}
	if !shouldCompactContext(983617, 1000000, settings) {
		t.Fatal("usage above context_window-reserve_tokens should compact")
	}
}

func TestEffectiveCompactionLimitsFitSmallContext(t *testing.T) {
	reserve, keepRecent := effectiveCompactionLimits(8192, CompactionSettings{})
	if reserve != 2048 || keepRecent != 4096 {
		t.Fatalf("limits = %d/%d, want 2048/4096", reserve, keepRecent)
	}
}

func TestRunAutomaticallyCompactsBeforeThresholdRequest(t *testing.T) {
	long := strings.Repeat("x", 9000)
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{
		chatResponse(NewTextMessage("assistant", "history checkpoint")),
		chatResponse(NewTextMessage("assistant", "turn-prefix checkpoint")),
		chatResponse(NewTextMessage("assistant", "final answer")),
	}}
	agent := NewAgent(Config{
		Provider:      llm,
		Tools:         commands.NewRegistry(),
		Model:         "custom",
		MaxTokens:     64,
		ContextWindow: 8192,
		Compaction: CompactionSettings{
			ReserveTokens:    40,
			KeepRecentTokens: 20,
		},
	})
	agent.LoadMessages([]*aop.Message{
		msg("user", long), msg("assistant", long),
		msg("user", long), msg("assistant", long),
	})

	result, err := agent.Run(context.Background(), TextInput("continue"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "final answer" {
		t.Fatalf("output = %q, want final answer", result.Output)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 3 {
		t.Fatalf("provider requests = %d, want history summary + turn-prefix summary + normal request", len(requests))
	}
	if got, want := requests[0].MaxTokens, 32; got != want {
		t.Fatalf("summary max_tokens = %d, want %d", got, want)
	}
	if got, want := requests[1].MaxTokens, 20; got != want {
		t.Fatalf("turn-prefix max_tokens = %d, want %d", got, want)
	}
	firstContent := provider.MessageText(result.Messages[0])
	if len(result.Messages) < 3 ||
		!strings.Contains(firstContent, "history checkpoint") ||
		!strings.Contains(firstContent, "turn-prefix checkpoint") {
		t.Fatalf("compacted messages = %#v", result.Messages)
	}
	if len(result.NewMessages) != 2 {
		t.Fatalf("new messages = %d, want run-scoped user/assistant pair: %#v", len(result.NewMessages), result.NewMessages)
	}
}

func TestRunRecoversFromContextOverflowOnce(t *testing.T) {
	long := strings.Repeat("x", 240)
	calls := 0
	llm := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		calls++
		switch calls {
		case 1:
			return nil, fmt.Errorf("prompt is too long: request exceeds maximum")
		case 2, 3:
			return chatResponse(NewTextMessage("assistant", "overflow checkpoint")), nil
		default:
			return chatResponse(NewTextMessage("assistant", "recovered")), nil
		}
	}}
	agent := NewAgent(Config{
		Provider:      llm,
		Tools:         commands.NewRegistry(),
		Model:         "custom",
		MaxTokens:     64,
		ContextWindow: 1000000,
		MaxRetries:    -1,
		Compaction: CompactionSettings{
			ReserveTokens:    40,
			KeepRecentTokens: 20,
		},
	})
	agent.LoadMessages([]*aop.Message{
		msg("user", long), msg("assistant", long),
		msg("user", long), msg("assistant", long),
	})

	result, err := agent.Run(context.Background(), TextInput("continue"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "recovered" || calls != 4 {
		t.Fatalf("output=%q calls=%d, want recovered/4", result.Output, calls)
	}
}

func TestCompactHistoryRejectsTruncatedSummary(t *testing.T) {
	long := strings.Repeat("x", 240)
	llm := &scriptedProvider{responses: []*ChatCompletionResponse{{
		Choices: []Choice{{
			Message:      NewTextMessage("assistant", "incomplete checkpoint").toAOP(),
			FinishReason: "max_tokens",
		}},
	}}}
	messages := []*aop.Message{
		msg("user", long), msg("assistant", long),
		msg("user", "recent"), msg("assistant", "reply"),
	}

	compacted, _, err := compactHistory(context.Background(), CompactConfig{
		Provider: llm, Model: "test", KeepRecentTokens: 20,
		ReserveTokens: 40, MaxTokens: 64,
	}, messages)
	if err == nil || !strings.Contains(err.Error(), "summary output truncated") {
		t.Fatalf("compactHistory() error = %v, want truncated summary error", err)
	}
	if compacted != nil {
		t.Fatalf("truncated summary replaced history: %#v", compacted)
	}
}

func TestContextOverflowDetectionExcludesRateLimits(t *testing.T) {
	if !isContextOverflowError(fmt.Errorf("input exceeds the context window")) {
		t.Fatal("expected context overflow detection")
	}
	if isContextOverflowError(fmt.Errorf("rate limit: too many tokens, retry later")) {
		t.Fatal("rate limit must not be treated as context overflow")
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
