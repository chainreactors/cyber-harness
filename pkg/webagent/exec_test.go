package webagent

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestHandleExecCompletesWithOutput(t *testing.T) {
	command := "printf hello"
	if runtime.GOOS == "windows" {
		command = "echo|set /p=hello"
	}
	payload, _ := json.Marshal(execPayload{Command: command, Timeout: 5})
	var messages []webproto.Message
	HandleExec(context.Background(), webproto.Message{TaskID: "exec-1", Payload: payload}, t.TempDir(), func(msg webproto.Message) {
		messages = append(messages, msg)
	})
	if len(messages) != 2 || messages[0].Type != "output" || messages[0].Data != "hello" || messages[1].Type != "complete" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestHandleExecReportsExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /b 7"
	}
	payload, _ := json.Marshal(execPayload{Command: command, Timeout: 5})
	var complete webproto.Message
	HandleExec(context.Background(), webproto.Message{TaskID: "exec-2", Payload: payload}, t.TempDir(), func(msg webproto.Message) {
		if msg.Type == "complete" {
			complete = msg
		}
	})
	var result execResult
	if err := json.Unmarshal(complete.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
}
