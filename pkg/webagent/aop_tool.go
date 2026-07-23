package webagent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
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
	GetTool(name string) (commands.AgentTool, bool)
}

// foregroundTool is implemented by tools that run a command in the foreground
// with streaming output, bypassing the agent-facing auto-background behavior.
type foregroundTool interface {
	RunForegroundTool(context.Context, string, commands.BashExecOptions) (tool.Result, error)
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
func HandleAOPToolCall(ctx context.Context, msg webproto.Message, executor aopToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent], send func(webproto.Message)) {
	var callEvent aop.Event
	if json.Unmarshal(msg.Payload, &callEvent) != nil {
		return
	}
	inbound, err := agent.Classify(callEvent)
	if err != nil || inbound.Kind != agent.InboundToolCall {
		return
	}
	handleAOPToolCall(ctx, msg, inbound, executor, dataBus, send)
}

func handleAOPToolCall(ctx context.Context, msg webproto.Message, inbound agent.Inbound, executor aopToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent], send func(webproto.Message)) {
	callEvent, call := inbound.Event, inbound.ToolCall
	if call.WorkDir != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkDir})
	}
	// Scanner telemetry published during the call is correlated to the hub
	// task through the call session id.
	ctx = output.ContextWithCallID(ctx, callEvent.SessionID)

	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, dataBus, callEvent.SessionID)
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
				args, err := commands.ParseArgs[commands.BashArgs](string(arguments))
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
