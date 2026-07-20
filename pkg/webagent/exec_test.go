package webagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type recordingBash struct {
	command string
	options commands.BashExecOptions
}

func (*recordingBash) Name() string                        { return "bash" }
func (*recordingBash) Description() string                 { return "test bash" }
func (*recordingBash) Definition() commands.ToolDefinition { return commands.ToolDefinition{} }
func (*recordingBash) Execute(context.Context, string) (commands.ToolResult, error) {
	return commands.ToolResult{}, nil
}
func (b *recordingBash) RunForeground(ctx context.Context, command string, options commands.BashExecOptions) (tmux.Info, error) {
	b.command = command
	b.options = options
	options.OnOutput([]byte("streamed\n"))
	output.PublishResult(ctx, &output.Result{Summary: output.Summary{Targets: 2}})
	return tmux.Info{State: tmux.StateCompleted}, nil
}

func TestExecCommandUsesBashForegroundExecution(t *testing.T) {
	reg := commands.NewRegistry()
	bash := &recordingBash{}
	reg.RegisterTool(bash)
	payload, _ := json.Marshal(webproto.ExecPayload{
		Command: "echo test", Cwd: "/workspace", Timeout: 7,
		Env: map[string]string{"TOKEN": "value"},
	})
	var messages []webproto.Message
	ExecCommand(context.Background(), webproto.Message{TaskID: "task-1", Payload: payload}, reg, func(message webproto.Message) {
		messages = append(messages, message)
	})

	if bash.command != "echo test" || bash.options.WorkDir != "/workspace" || bash.options.Timeout != 7*time.Second {
		t.Fatalf("bash options = %+v", bash.options)
	}
	if bash.options.Env["TOKEN"] != "value" {
		t.Fatalf("bash policy options = %+v", bash.options)
	}
	if len(messages) != 2 || messages[0].Type != "output" || messages[1].Type != "complete" {
		t.Fatalf("messages = %#v", messages)
	}
	var structured output.Result
	if err := json.Unmarshal(messages[1].Payload, &structured); err != nil {
		t.Fatalf("decode structured result: %v", err)
	}
	if messages[1].Data != "" || structured.Summary.Targets != 2 {
		t.Fatalf("complete = %#v", messages[1])
	}
}
