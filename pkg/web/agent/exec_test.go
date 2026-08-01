package agent

import (
	"context"
	"runtime"
	"testing"

	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func TestExecRequestCompletesWithOutput(t *testing.T) {
	command := "printf hello"
	if runtime.GOOS == "windows" {
		command = "echo|set /p=hello"
	}
	var frames []*transport.AgentFrame
	handleExecRequest(context.Background(), &transport.ExecRequest{TaskId: "exec-1", Command: command, TimeoutSeconds: 5}, t.TempDir(), func(frame *transport.AgentFrame) { frames = append(frames, frame) })
	if len(frames) != 2 || string(frames[0].GetExecOutput().Data) != "hello" || frames[1].GetExecResult().State != "completed" {
		t.Fatalf("unexpected frames: %#v", frames)
	}
}

func TestExecRequestReportsExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /b 7"
	}
	var result *transport.ExecResult
	handleExecRequest(context.Background(), &transport.ExecRequest{TaskId: "exec-2", Command: command, TimeoutSeconds: 5}, t.TempDir(), func(frame *transport.AgentFrame) {
		if frame.GetExecResult() != nil {
			result = frame.GetExecResult()
		}
	})
	if result == nil || result.ExitCode != 7 {
		t.Fatalf("result = %+v, want exit code 7", result)
	}
}
