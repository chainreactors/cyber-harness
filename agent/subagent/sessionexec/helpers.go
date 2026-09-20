package sessionexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/proto"
)

func resultOutput(r *agent.Result) string {
	if r == nil {
		return ""
	}
	return r.Output
}

func subagentCompletion(r *agent.Result, err error) (string, string) {
	result := resultOutput(r)
	if err == nil {
		return "completed", result
	}
	status := "failed"
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timed_out"
	} else if errors.Is(err, context.Canceled) {
		status = "canceled"
	}
	if result != "" {
		return status, fmt.Sprintf("Error: %s\n\nPartial output:\n%s", err, result)
	}
	return status, fmt.Sprintf("Error: %s", err)
}

// A fork cannot inherit only some results of a parallel tool-call batch.
func truncateToLastCompleteBoundary(messages []*aop.Message) []*aop.Message {
	pending := make(map[string]bool)
	boundary := 0
	for index, message := range messages {
		if message == nil {
			continue
		}
		for _, call := range provider.MessageToolCalls(message) {
			pending[call.Id] = true
		}
		for _, content := range message.Content {
			if result := content.GetToolResult(); result != nil {
				delete(pending, result.CallId)
			}
		}
		if len(pending) == 0 {
			boundary = index + 1
		}
	}
	result := make([]*aop.Message, boundary)
	for i, message := range messages[:boundary] {
		result[i] = proto.CloneOf(message)
	}
	return result
}
