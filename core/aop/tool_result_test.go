package aop

import (
	"errors"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/tool"
)

func TestToolResultDataFromResultPreservesStructuredContent(t *testing.T) {
	result := tool.Result{
		Content: []tool.ContentBlock{
			tool.TextBlock("done"),
			tool.ImageBlock("image/png", "aGVsbG8="),
		},
		Details: map[string]any{"ports": 3}, Terminate: true,
	}
	data := ToolResultDataFromResult(ToolCallData{ToolCallID: "call-1", ToolName: "scan"}, result, nil, 12*time.Millisecond)
	if data.ToolCallID != "call-1" || data.ToolName != "scan" || data.DurationMs != 12 || !data.Terminate || data.IsError {
		t.Fatalf("data = %+v", data)
	}
	content, ok := data.Content.(ToolResultContent)
	if !ok || content.Content != "done" || len(content.Images) != 1 || content.Images[0].MediaType != "image/png" {
		t.Fatalf("content = %#v", data.Content)
	}
}

func TestToolResultDataFromResultUsesExecutionError(t *testing.T) {
	data := ToolResultDataFromResult(ToolCallData{ToolCallID: "call-1"}, tool.TextResult("partial"), errors.New("failed"), 0)
	if !data.IsError || ToolResultText(data.Content) != "failed" {
		t.Fatalf("data = %+v", data)
	}
}
