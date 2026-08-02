package agent

import (
	"context"
	"runtime"
	"testing"

	execpb "github.com/chainreactors/aiscan/aop/exec"
	protobuf "google.golang.org/protobuf/proto"
)

func TestExecRequestCompletesWithOutput(t *testing.T) {
	command := "printf hello"
	if runtime.GOOS == "windows" {
		command = "echo|set /p=hello"
	}
	var messages []*execpb.ProtocolMessage
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-1", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok {
			messages = append(messages, value)
		}
	})
	if len(messages) != 2 || string(messages[0].GetOutput().Data) != "hello" || messages[1].GetResult().State != "completed" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestExecRequestReportsExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /b 7"
	}
	var result *execpb.Result
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-2", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok && value.GetResult() != nil {
			result = value.GetResult()
		}
	})
	if result == nil || result.ExitCode != 7 {
		t.Fatalf("result = %+v, want exit code 7", result)
	}
}
