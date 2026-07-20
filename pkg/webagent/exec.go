package webagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// ExecCommand executes every command through the registered BashTool. BashTool
// is the single policy boundary for shell and in-process command execution.
func ExecCommand(ctx context.Context, msg webproto.Message, reg *commands.CommandRegistry, send func(webproto.Message)) {
	taskID := msg.TaskID

	// Parse structured payload; fall back to Data for backward compat.
	var ep webproto.ExecPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &ep)
	}
	if ep.Command == "" {
		ep.Command = strings.TrimSpace(msg.Data)
	}
	if ep.Command == "" {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "empty command"})
		return
	}

	// Scope scanner telemetry to this remote execution. Final structured data
	// returns normally on Execution.Details.
	execCtx := output.ContextWithCallID(ctx, taskID)
	writer := &StreamWriter{TaskID: taskID, SendFn: send}
	bash, ok := reg.GetTool("bash")
	if !ok {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "bash tool is not registered"})
		return
	}
	runner, ok := bash.(interface {
		RunForeground(context.Context, string, commands.BashExecOptions) (*commands.Execution, error)
	})
	if !ok {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "registered bash tool does not support controlled execution"})
		return
	}

	result, err := runner.RunForeground(execCtx, ep.Command, commands.BashExecOptions{
		WorkDir: ep.Cwd,
		Env:     ep.Env,
		Timeout: time.Duration(ep.Timeout) * time.Second,
		OnOutput: func(data []byte) {
			_, _ = writer.Write(data)
		},
	})
	writer.Flush()
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
		return
	}
	data := ""
	if result.ExitCode != 0 {
		data = fmt.Sprintf("exit %d", result.ExitCode)
	}
	var payload json.RawMessage
	if result.Details != nil {
		payload, _ = json.Marshal(result.Details)
	}
	send(webproto.Message{Type: "complete", TaskID: taskID, Data: data, Payload: payload})
}
