package webagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

type execPayload struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type execResult struct {
	ExitCode  int    `json:"exit_code"`
	State     string `json:"state,omitempty"`
	KillCause string `json:"kill_cause,omitempty"`
}

type execStreamPayload struct {
	Stream string `json:"stream"`
}

// HandleExec runs Cairn's native shell RPC and returns output using the same
// correlated output/complete envelope as the file RPCs.
func HandleExec(ctx context.Context, msg webproto.Message, baseDir string, send func(webproto.Message)) {
	var payload execPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Command == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "command required"})
		return
	}

	runCtx := ctx
	cancel := func() {}
	if payload.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(payload.Timeout)*time.Second)
	}
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd.exe", "/C", payload.Command)
	} else {
		cmd = exec.CommandContext(runCtx, "/bin/sh", "-c", payload.Command)
	}
	if payload.Cwd != "" {
		cmd.Dir = resolveFileRPCPath(baseDir, payload.Cwd)
	} else if baseDir != "" {
		cmd.Dir = baseDir
	}
	cmd.Env = os.Environ()
	for key, value := range payload.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.Len() > 0 {
		send(webproto.Message{Type: "output", TaskID: msg.TaskID, Data: stdout.String(), Payload: webproto.MustJSON(execStreamPayload{Stream: "stdout"})})
	}
	if stderr.Len() > 0 {
		send(webproto.Message{Type: "output", TaskID: msg.TaskID, Data: stderr.String(), Payload: webproto.MustJSON(execStreamPayload{Stream: "stderr"})})
	}

	result := execResult{State: "completed"}
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.ExitCode = -1
			result.State = "killed"
			result.KillCause = "timeout"
		case errors.Is(runCtx.Err(), context.Canceled):
			result.ExitCode = -1
			result.State = "killed"
			result.KillCause = "cancelled"
		case errors.As(err, &exitErr):
			result.ExitCode = exitErr.ExitCode()
		default:
			send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
			return
		}
	}
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(result)})
}
