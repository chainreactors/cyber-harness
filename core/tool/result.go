package tool

import (
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
)

// Result is the value returned by Tool.Execute — the AOP tool result proto.
type Result = aop.ToolResult

func ResultText(r *Result) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	for _, block := range r.Output {
		if text := block.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func ResultHasImages(r *Result) bool {
	if r == nil {
		return false
	}
	for _, block := range r.Output {
		if media := block.GetMedia(); media != nil && media.Kind == "image" {
			return true
		}
	}
	return false
}

func TextResult(s string) *Result {
	return &Result{Output: []*aop.Content{aop.Text(s)}}
}

func ErrorResult(msg string) *Result {
	return &Result{Output: []*aop.Content{aop.Text(msg)}, IsError: true}
}

func TerminateResult(s string) *Result {
	return &Result{Output: []*aop.Content{aop.Text(s)}, Terminate: true}
}
