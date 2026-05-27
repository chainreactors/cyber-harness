package command

import "strings"

type ToolResult struct {
	Content   []ContentBlock
	IsError   bool
	Terminate bool
}

// Text returns all text content blocks concatenated, for backward
// compatibility with code that expects a plain string.
func (r ToolResult) Text() string {
	var sb strings.Builder
	for _, block := range r.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

func TextResult(s string) ToolResult {
	return ToolResult{Content: []ContentBlock{TextBlock(s)}}
}

func ErrorResult(msg string) ToolResult {
	return ToolResult{Content: []ContentBlock{TextBlock(msg)}, IsError: true}
}

func TerminateResult(s string) ToolResult {
	return ToolResult{Content: []ContentBlock{TextBlock(s)}, Terminate: true}
}
