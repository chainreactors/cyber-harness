package tool

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/operation"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ExecuteToolRequest runs one canonical AOP tool call against the executor
// and wraps the outcome as a ToolResult event correlated to operationID.
func ExecuteToolRequest(ctx context.Context, operationID string, request *toolpb.Call, executor Executor, progressBus *eventbus.Bus[*toolpb.Progress]) (*aop.Event, error) {
	if request == nil || request.Call == nil || operationID == "" {
		return nil, fmt.Errorf("tool call correlation is invalid")
	}
	call := request.Call
	if call.Id == "" {
		call.Id = operationID
	}
	if call.Id != operationID {
		return nil, fmt.Errorf("tool call id must match envelope id")
	}
	if strings.TrimSpace(call.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	emitter := operation.InvocationFromContext(ctx).Emitter
	if strings.TrimSpace(emitter) == "" {
		emitter = "tool"
	}
	progress := progressStreamer{bus: progressBus, tool: call.Name, callID: operationID}
	ctx = operation.ContextWithInvocation(ctx, operation.Invocation{
		WorkDir: call.WorkingDirectory, CallID: operationID,
		SessionID: request.SessionId, TurnID: request.TurnId, Emitter: emitter,
		Progress: progress.Write,
	})
	arguments := call.GetArguments().GetData()
	if len(arguments) == 0 {
		arguments = []byte("{}")
	}
	started := time.Now()
	result, execErr := executor.ExecuteTool(ctx, call.Name, string(arguments))
	progress.Flush()
	if result == nil {
		result = &aop.ToolResult{}
	}
	if execErr != nil {
		result.IsError = true
		result.Output = []*aop.Content{aop.Text(execErr.Error())}
	}
	result.CallId = call.Id
	result.Name = call.Name
	result.DurationMs = uint64(time.Since(started).Milliseconds())
	sanitizeToolResultUTF8(result)
	// Bound inline output to the model-context budget; oversized results are
	// spilled to disk and replaced with a preview plus a reference.
	boundToolResultOutput(ctx, result)
	return &aop.Event{
		Id: aop.EnvelopeID(), EmittedAt: timestamppb.Now(), SessionId: request.SessionId,
		TurnId: request.TurnId, Emitter: emitter, Payload: &aop.Event_ToolResult{ToolResult: result},
	}, nil
}

// progressStreamer splits raw command output into lines and publishes each
// non-blank line as ephemeral tool progress. Its buffer belongs to one request;
// it neither executes tools nor manages their lifetime.
type progressStreamer struct {
	bus    *eventbus.Bus[*toolpb.Progress]
	tool   string
	callID string
	buf    []byte
}

// maxProgressBuf is the maximum buffer size before a progressStreamer flushes.
const maxProgressBuf = 64 << 10

func (s *progressStreamer) Write(p []byte) {
	if s.bus == nil {
		return
	}
	s.buf = append(s.buf, p...)
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx < 0 {
			if len(s.buf) >= maxProgressBuf {
				s.Flush()
			}
			return
		}
		line := sanitizeUTF8(string(s.buf[:idx]))
		s.buf = s.buf[idx+1:]
		s.emit(line)
	}
}

func (s *progressStreamer) Flush() {
	if s.bus == nil || len(s.buf) == 0 {
		return
	}
	data := sanitizeUTF8(string(s.buf))
	s.buf = s.buf[:0]
	s.emit(data)
}

func sanitizeUTF8(value string) string {
	return strings.ToValidUTF8(value, "\uFFFD")
}

// sanitizeToolResultUTF8 protects the AOP boundary from arbitrary command
// bytes and tools that construct protobuf content directly.
func sanitizeToolResultUTF8(result *aop.ToolResult) {
	if result == nil {
		return
	}
	result.CallId = sanitizeUTF8(result.CallId)
	result.Name = sanitizeUTF8(result.Name)
	for _, content := range result.Output {
		if content == nil {
			continue
		}
		switch value := content.Value.(type) {
		case *aop.Content_Text:
			if value.Text != nil {
				value.Text.Text = sanitizeUTF8(value.Text.Text)
			}
		case *aop.Content_Reasoning:
			if value.Reasoning != nil {
				value.Reasoning.Text = sanitizeUTF8(value.Reasoning.Text)
			}
		case *aop.Content_Refusal:
			value.Refusal = sanitizeUTF8(value.Refusal)
		case *aop.Content_ToolResult:
			sanitizeToolResultUTF8(value.ToolResult)
		}
	}
}

func (s *progressStreamer) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	s.bus.Emit(&toolpb.Progress{
		Tool: s.tool, Text: line, CallId: s.callID, Timestamp: timestamppb.New(time.Now()),
	})
}
