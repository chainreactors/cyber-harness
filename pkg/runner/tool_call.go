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
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ToolExecutor is the minimal surface a tool call dispatcher needs.
// *commands.CommandRegistry implements it.
type ToolExecutor interface {
	ExecuteTool(context.Context, string, string) (*tool.Result, error)
}

// ExecuteToolRequest runs one canonical AOP tool call against the executor
// and wraps the outcome as a ToolResult event correlated to operationID.
func ExecuteToolRequest(ctx context.Context, operationID string, request *toolpb.Call, executor ToolExecutor, progressBus *eventbus.Bus[*toolpb.Progress]) (*aop.Event, error) {
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
	ctx = tool.ContextWithInvocation(ctx, tool.Invocation{
		WorkDir: call.WorkingDirectory, CallID: operationID,
		SessionID: request.SessionId, TurnID: request.TurnId, Emitter: "aiscan.agent",
	})
	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, progressBus, operationID)
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

// executeCall runs the tool call. Tools with foreground capability publish
// ephemeral progress while running; all other tools take the plain ExecuteTool path.
func executeCall(ctx context.Context, executor ToolExecutor, call *aop.ToolCall, progressBus *eventbus.Bus[*toolpb.Progress], callID string) (*tool.Result, error) {
	arguments := call.GetArguments().GetData()
	if len(arguments) == 0 {
		arguments = []byte("{}")
	}
	if registry, ok := executor.(*commands.CommandRegistry); ok && call.Name == "bash" {
		args, err := tool.ParseArgs[commands.BashArgs](string(arguments))
		if err != nil {
			return nil, err
		}
		if err := args.Validate(); err != nil {
			return nil, err
		}
		progress := newProgressStreamer(progressBus, call.Name, callID)
		options := commands.BashExecOptions{OnOutput: progress.Write}
		if args.TimeoutSpecified() {
			options.Timeout = time.Duration(args.Timeout) * time.Second
			options.TimeoutSet = true
		}
		result, err := registry.ExecuteBashForeground(ctx, args.Command, options)
		progress.Flush()
		return result, err
	}
	return executor.ExecuteTool(ctx, call.Name, string(arguments))
}

// progressStreamer splits raw command output into lines and publishes each
// non-blank line as ephemeral tool progress.
type progressStreamer struct {
	bus    *eventbus.Bus[*toolpb.Progress]
	tool   string
	callID string
	buf    []byte
}

// maxProgressBuf is the maximum buffer size before a progressStreamer flushes.
const maxProgressBuf = 64 << 10

func newProgressStreamer(bus *eventbus.Bus[*toolpb.Progress], tool, callID string) *progressStreamer {
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
	s.bus.Emit(&toolpb.Progress{
		Tool: s.tool, Text: line, CallId: s.callID, Timestamp: timestamppb.New(time.Now()),
	})
}
