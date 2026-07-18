package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// ExecCommand executes a command via the command registry or falls back to the
// system shell. It streams output and sends a final complete/error message.
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

	if ep.Cwd != "" {
		reg.SetWorkDir(ep.Cwd)
	}

	// Scope scanner telemetry/SCO output to the Cairn RPC that launched it.
	execCtx := output.ContextWithCallID(ctx, taskID)
	if ep.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(ep.Timeout)*time.Second)
		defer cancel()
	}

	tokens, err := commands.SplitCommandLine(ep.Command)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
		return
	}
	if len(tokens) == 0 {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "empty command"})
		return
	}

	writer := &StreamWriter{TaskID: taskID, SendFn: send}

	// Try registered command (aiscan tools: scan, gogo, spray, etc.)
	if cmd, ok := reg.Get(tokens[0]); ok {
		if sc, ok := cmd.(interface {
			ExecuteStructured(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error)
		}); ok {
			out, result, err := sc.ExecuteStructured(execCtx, tokens[1:], writer)
			writer.Flush()
			if err != nil {
				send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
				return
			}
			var payload json.RawMessage
			if result != nil {
				payload, _ = json.Marshal(result)
			}
			send(webproto.Message{Type: "complete", TaskID: taskID, Data: out, Payload: payload})
			return
		}
		out, err := reg.ExecuteArgsStreaming(execCtx, tokens, writer)
		writer.Flush()
		if err != nil {
			send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
			return
		}
		send(webproto.Message{Type: "complete", TaskID: taskID, Data: out})
		return
	}

	// Shell fallback.
	ShellExec(execCtx, taskID, ep, send)
}

// ShellExec runs a command via the system shell and reports results over the
// WebSocket send function.
func ShellExec(ctx context.Context, taskID string, ep webproto.ExecPayload, send func(webproto.Message)) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", ep.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", ep.Command)
	}
	if ep.Cwd != "" {
		cmd.Dir = ep.Cwd
	}
	cmd.Env = os.Environ()
	for key, value := range ep.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		send(webproto.Message{Type: "output", TaskID: taskID, Data: string(out)})
	}
	if err != nil {
		code := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		send(webproto.Message{Type: "complete", TaskID: taskID, Data: fmt.Sprintf("exit %d", code)})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: taskID})
}
