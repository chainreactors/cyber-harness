package webagent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type aopToolExecutor interface {
	ExecuteTool(context.Context, string, string) (tool.Result, error)
}

// IsAOPToolCall reports whether msg carries a valid AOP tool.call event.
func IsAOPToolCall(msg webproto.Message) bool {
	if msg.Type != "aop" || len(msg.Payload) == 0 {
		return false
	}
	var event aop.Event
	return json.Unmarshal(msg.Payload, &event) == nil && event.Valid() && event.Type == aop.TypeToolCall
}

// HandleAOPToolCall executes the structured call and returns an AOP
// tool.result. Transport failures are represented as is_error results so the
// agent always observes one terminal event for every accepted tool.call.
func HandleAOPToolCall(ctx context.Context, msg webproto.Message, executor aopToolExecutor, send func(webproto.Message)) {
	var callEvent aop.Event
	if json.Unmarshal(msg.Payload, &callEvent) != nil {
		return
	}
	inbound, err := agent.Classify(callEvent)
	if err != nil || inbound.Kind != agent.InboundToolCall {
		return
	}
	handleAOPToolCall(ctx, msg, inbound, executor, send)
}

func handleAOPToolCall(ctx context.Context, msg webproto.Message, inbound agent.Inbound, executor aopToolExecutor, send func(webproto.Message)) {
	callEvent, call := inbound.Event, inbound.ToolCall
	if call.WorkDir != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkDir})
	}

	arguments, err := json.Marshal(call.Args)
	if err != nil {
		arguments = []byte("{}")
	}
	started := time.Now()
	result, execErr := executor.ExecuteTool(ctx, call.ToolName, string(arguments))
	resultData := aop.ToolResultData{
		ToolCallID: call.ToolCallID,
		ToolName:   call.ToolName,
		DurationMs: int(time.Since(started).Milliseconds()),
	}
	if execErr != nil {
		resultData.Content = execErr.Error()
		resultData.IsError = true
	} else {
		resultData.Content = result.Text()
		if result.HasImages() {
			content := aop.ToolResultContent{Content: result.Text()}
			for _, block := range result.Content {
				if block.Type == "image" {
					content.Images = append(content.Images, aop.ImageSource{Base64: block.Base64Data, MediaType: block.MimeType})
				}
			}
			resultData.Content = content
		}
		if result.Details != nil {
			resultData.Details = result.Details
		}
		resultData.Terminate = result.Terminate
		resultData.IsError = result.IsError
	}
	data, _ := json.Marshal(resultData)
	resultEvent := aop.Event{
		Type:      aop.TypeToolResult,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: callEvent.SessionID,
		Agent:     callEvent.Agent,
		Data:      data,
		Ext:       callEvent.Ext,
	}
	payload, _ := json.Marshal(resultEvent)
	taskID := msg.TaskID
	if taskID == "" {
		taskID = call.ToolCallID
	}
	send(webproto.Message{Type: "aop", TaskID: taskID, Payload: payload})
}
