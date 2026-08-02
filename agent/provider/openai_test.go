package provider

import (
	"encoding/json"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
)

func TestMarshalOpenAIRequestAlwaysIncludesMessageContent(t *testing.T) {
	toolCall := &aop.Content{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{
		Id:   "call-1",
		Name: "empty_result",
		Kind: "function",
	}}}
	emptyToolResult := &aop.Content{Value: &aop.Content_ToolResult{ToolResult: &aop.ToolResult{
		CallId: "call-1",
		Name:   "empty_result",
	}}}

	body, err := marshalOpenAIRequest(&ChatCompletionRequest{
		Model: "test",
		Messages: []*aop.Message{
			{Role: "assistant", Content: []*aop.Content{toolCall}},
			{Role: "tool", Content: []*aop.Content{emptyToolResult}},
		},
	})
	if err != nil {
		t.Fatalf("marshalOpenAIRequest() error = %v", err)
	}

	var request struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(request.Messages))
	}
	for i, message := range request.Messages {
		content, ok := message["content"]
		if !ok {
			t.Fatalf("messages[%d] is missing content: %s", i, body)
		}
		if string(content) != `""` {
			t.Fatalf("messages[%d].content = %s, want empty string", i, content)
		}
	}
}
