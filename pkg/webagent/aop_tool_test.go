package webagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type aopTestExecutor struct{}

func (aopTestExecutor) ExecuteTool(_ context.Context, name, arguments string) (tool.Result, error) {
	return tool.TextResult(name + ":" + arguments), nil
}

func TestHandleAOPToolCall(t *testing.T) {
	callData, _ := json.Marshal(aop.ToolCallData{
		ToolCallID: "call-1",
		ToolName:   "echo",
		Args:       map[string]any{"value": "hello"},
	})
	call := aop.Event{
		Type: aop.TypeToolCall, TS: "2026-07-21T00:00:00Z",
		SessionID: "session-1", Agent: "cairn.explore", Data: callData,
	}
	payload, _ := json.Marshal(call)
	var got webproto.Message
	HandleAOPToolCall(context.Background(), webproto.Message{
		Type: "aop", TaskID: "call-1", Payload: payload,
	}, aopTestExecutor{}, func(msg webproto.Message) { got = msg })

	if got.Type != "aop" || got.TaskID != "call-1" {
		t.Fatalf("result envelope = %+v", got)
	}
	var event aop.Event
	if err := json.Unmarshal(got.Payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != aop.TypeToolResult || event.SessionID != call.SessionID || event.Agent != call.Agent {
		t.Fatalf("result event = %+v", event)
	}
	var result aop.ToolResultData
	if err := json.Unmarshal(event.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.ToolCallID != "call-1" || result.ToolName != "echo" || result.IsError {
		t.Fatalf("result data = %+v", result)
	}
}
