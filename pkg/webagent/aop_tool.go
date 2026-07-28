package webagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type aopToolExecutor interface {
	ExecuteTool(context.Context, string, string) (tool.Result, error)
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
	RunForegroundTool(context.Context, string, commands.BashExecOptions) (tool.Result, error)
}

// HandleToolCallEvent executes one direct AOP tool.call and returns its
// terminal tool.result through the same AOP envelope.
func HandleToolCallEvent(ctx context.Context, msg webproto.Message, event aop.Event, executor aopToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent], send func(webproto.Message)) {
	sendError := func(err error) {
		payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
		send(webproto.Message{Type: webproto.TypeError, TaskID: msg.TaskID, Payload: payload})
	}
	if !event.Valid() {
		sendError(fmt.Errorf("invalid inbound AOP event"))
		return
	}
	if event.Type != aop.TypeToolCall {
		sendError(fmt.Errorf("unsupported inbound AOP event %q", event.Type))
		return
	}
	call, err := aop.DecodeData[aop.ToolCallData](event)
	if err != nil {
		sendError(fmt.Errorf("decode tool.call: %w", err))
		return
	}
	if msg.TaskID == "" || call.ToolCallID != msg.TaskID {
		sendError(fmt.Errorf("tool.call correlation requires task_id == tool_call_id"))
		return
	}
	if strings.TrimSpace(call.ToolName) == "" {
		sendError(fmt.Errorf("tool.call tool_name is required"))
		return
	}
	if call.WorkDir != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkDir})
	}
	callID := msg.TaskID
	ctx = output.ContextWithCallID(ctx, callID)

	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, dataBus, callID)
	data := aop.ToolResultDataFromResult(call, result, execErr, time.Since(started))
	raw, _ := json.Marshal(data)
	event.Type = aop.TypeToolResult
	event.TS = time.Now().UTC().Format(time.RFC3339Nano)
	event.Data = raw
	payload, _ := json.Marshal(event)
	send(webproto.Message{Type: webproto.TypeAOP, TaskID: callID, TurnID: event.TurnID, Payload: payload})
}

// executeCall runs the tool call. Tools with foreground capability stream
// stdout lines as tool.data progress events on dataBus while running; all
// other tools take the plain ExecuteTool path.
func executeCall(ctx context.Context, executor aopToolExecutor, call aop.ToolCallData, dataBus *eventbus.Bus[output.ToolDataEvent], callID string) (tool.Result, error) {
	arguments, err := json.Marshal(call.Args)
	if err != nil {
		arguments = []byte("{}")
	}
	if resolver, ok := executor.(toolResolver); ok {
		if resolved, ok := resolver.GetTool(call.ToolName); ok {
			if fg, ok := resolved.(foregroundTool); ok {
				args, err := tool.ParseArgs[commands.BashArgs](string(arguments))
				if err != nil {
					return tool.Result{}, err
				}
				progress := newProgressStreamer(dataBus, call.ToolName, callID)
				result, err := fg.RunForegroundTool(ctx, args.Command, commands.BashExecOptions{
					Timeout:  time.Duration(args.Timeout) * time.Second,
					OnOutput: progress.Write,
				})
				progress.Flush()
				return result, err
			}
		}
	}
	return executor.ExecuteTool(ctx, call.ToolName, string(arguments))
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
