package aop

import (
	"fmt"
	"time"

	"github.com/chainreactors/aiscan/core/tool"
)

// ToolResultContentFromResult converts the canonical tool Result blocks to the
// AOP content variant without flattening images or changing the supplied text.
func ToolResultContentFromResult(result tool.Result, text string) any {
	if !result.HasImages() {
		return text
	}
	content := ToolResultContent{Content: text}
	for _, block := range result.Content {
		if block.Type == "image" {
			content.Images = append(content.Images, ImageSource{Base64: block.Base64Data, MediaType: block.MimeType})
		}
	}
	return content
}

// ToolResultDataFromResult is the single conversion used by Agent-internal and
// direct remote tool execution.
func ToolResultDataFromResult(call ToolCallData, result tool.Result, execErr error, duration time.Duration) ToolResultData {
	text := result.Text()
	if execErr != nil {
		text = execErr.Error()
	}
	return ToolResultData{
		ToolCallID: call.ToolCallID,
		ToolName:   call.ToolName,
		Content:    ToolResultContentFromResult(result, text),
		Details:    result.Details,
		Terminate:  result.Terminate,
		IsError:    execErr != nil || result.IsError,
		DurationMs: int(duration.Milliseconds()),
	}
}

// ToolResultText reads both in-memory and JSON-decoded structured content.
func ToolResultText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case ToolResultContent:
		return value.Content
	case *ToolResultContent:
		if value != nil {
			return value.Content
		}
	case map[string]any:
		if text, ok := value["content"].(string); ok {
			return text
		}
	}
	return fmt.Sprint(content)
}
