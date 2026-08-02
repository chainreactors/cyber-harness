package runner

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ToolExecutor is the minimal surface a tool call dispatcher needs.
// *commands.CommandRegistry implements it.
type ToolExecutor interface {
	ExecuteTool(context.Context, string, string) (*tool.Result, error)
}

// toolResolver is an optional executor capability exposing the concrete tool
// catalog, so the transport can assert execution capabilities on the resolved
// tool instead of its name. *commands.CommandRegistry implements it.
type toolResolver interface {
	GetTool(name string) (tool.Tool, bool)
}

// foregroundTool is implemented by tools that run a command in the foreground
// with streaming output, bypassing the agent-facing auto-background behavior.
type foregroundTool interface {
	RunForegroundTool(context.Context, string, commands.BashExecOptions) (*tool.Result, error)
}

// ExecuteToolRequest runs one canonical AOP tool call against the executor
// and wraps the outcome as a ToolResult event correlated to operationID.
func ExecuteToolRequest(ctx context.Context, operationID string, request *toolpb.Call, executor ToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent]) (*aop.Event, error) {
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
	if call.WorkingDirectory != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkingDirectory})
	}
	ctx = output.ContextWithCallID(ctx, operationID)
	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, dataBus, operationID)
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
	return &aop.Event{
		Id: runtimeEnvelopeID(), EmittedAt: timestamppb.Now(), SessionId: request.SessionId,
		TurnId: request.TurnId, Emitter: "aiscan.agent", Payload: &aop.Event_ToolResult{ToolResult: result},
	}, nil
}

// executeCall runs the tool call. Tools with foreground capability stream
// stdout lines as tool.data progress events on dataBus while running; all
// other tools take the plain ExecuteTool path.
func executeCall(ctx context.Context, executor ToolExecutor, call *aop.ToolCall, dataBus *eventbus.Bus[output.ToolDataEvent], callID string) (*tool.Result, error) {
	arguments := call.GetArguments().GetData()
	if len(arguments) == 0 {
		arguments = []byte("{}")
	}
	if resolver, ok := executor.(toolResolver); ok {
		if resolved, ok := resolver.GetTool(call.Name); ok {
			if fg, ok := resolved.(foregroundTool); ok {
				args, err := tool.ParseArgs[commands.BashArgs](string(arguments))
				if err != nil {
					return nil, err
				}
				progress := newProgressStreamer(dataBus, call.Name, callID)
				result, err := fg.RunForegroundTool(ctx, args.Command, commands.BashExecOptions{
					Timeout:  time.Duration(args.Timeout) * time.Second,
					OnOutput: progress.Write,
				})
				progress.Flush()
				return result, err
			}
		}
	}
	return executor.ExecuteTool(ctx, call.Name, string(arguments))
}

// progressStreamer splits raw command output into lines and publishes each
// non-blank line as a tool.data progress event.
type progressStreamer struct {
	bus    *eventbus.Bus[output.ToolDataEvent]
	tool   string
	callID string
	buf    []byte
}

// maxProgressBuf is the maximum buffer size before a progressStreamer flushes.
const maxProgressBuf = 64 << 10

func newProgressStreamer(bus *eventbus.Bus[output.ToolDataEvent], tool, callID string) *progressStreamer {
	return &progressStreamer{bus: bus, tool: tool, callID: callID}
}

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
		line := string(s.buf[:idx])
		s.buf = s.buf[idx+1:]
		s.emit(line)
	}
}

func (s *progressStreamer) Flush() {
	if s.bus == nil || len(s.buf) == 0 {
		return
	}
	data := string(s.buf)
	s.buf = s.buf[:0]
	s.emit(data)
}

func (s *progressStreamer) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	s.bus.Emit(output.ToolDataEvent{
		Tool:      s.tool,
		Kind:      output.ToolDataProgress,
		Data:      line,
		CallID:    s.callID,
		Timestamp: time.Now(),
	})
}
