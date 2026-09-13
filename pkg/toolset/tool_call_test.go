package toolset

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/operation"
	"github.com/chainreactors/aiscan/core/tool"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
	"github.com/chainreactors/aiscan/pkg/commands"
)

type aopTestExecutor struct{}

func (aopTestExecutor) Execute(_ context.Context, arguments string) (*tool.Result, error) {
	return tool.TextResult("echo:" + arguments), nil
}

type structuredResultExecutor struct {
	err error
}

func (e structuredResultExecutor) Execute(context.Context, string) (*tool.Result, error) {
	return &tool.Result{
		Output: []*aop.Content{
			aop.Text("partial"),
			aop.Image("image/png", []byte("image")),
		},
		IsError:   e.err == nil,
		Terminate: true,
	}, e.err
}

func toolRequest(t *testing.T, id, name string, arguments map[string]any) *toolpb.Call {
	t.Helper()
	value, err := aop.JSONValue(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return &toolpb.Call{SessionId: "session-1", TurnId: "turn-1", Call: &aop.ToolCall{Id: id, Name: name, Arguments: value}}
}

func TestExecuteToolRequestPreservesStructuredResult(t *testing.T) {
	event, err := ExecuteToolRequest(context.Background(), "call-structured", toolRequest(t, "call-structured", "scan", nil), testRegistry(t, structuredResultExecutor{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if !result.IsError || !result.Terminate || result.DurationMs > uint64(time.Minute.Milliseconds()) {
		t.Fatalf("result flags = %+v", result)
	}
	if len(result.Output) != 2 || result.Output[0].GetText().GetText() != "partial" || string(result.Output[1].GetMedia().GetResource().GetData()) != "image" {
		t.Fatalf("result output = %+v", result.Output)
	}
}

func TestExecuteToolRequestUsesExecutionErrorText(t *testing.T) {
	event, err := ExecuteToolRequest(context.Background(), "call-error", toolRequest(t, "call-error", "scan", nil), testRegistry(t, structuredResultExecutor{err: errors.New("failed")}), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if !result.IsError || result.Output[0].GetText().GetText() != "failed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteToolRequest(t *testing.T) {
	event, err := ExecuteToolRequest(context.Background(), "call-1", toolRequest(t, "call-1", "echo", map[string]any{"value": "hello"}), testRegistry(t, aopTestExecutor{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	if result.CallId != "call-1" || result.Name != "echo" || !strings.Contains(result.Output[0].GetText().Text, "echo") {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteToolRequestRejectsMismatchedCorrelation(t *testing.T) {
	request := toolRequest(t, "call-1", "echo", map[string]any{"value": "hello"})
	if _, err := ExecuteToolRequest(context.Background(), "other", request, testRegistry(t, aopTestExecutor{}), nil); err == nil {
		t.Fatal("expected correlation error")
	}
}

type recordingBash struct {
	command string
	options commands.BashExecOptions
}

func (*recordingBash) Name() string        { return "bash" }
func (*recordingBash) Description() string { return "test bash" }
func (*recordingBash) Definition() *tool.Definition {
	return tool.Def("bash", "test bash", struct {
		Command string `json:"command"`
	}{})
}
func (b *recordingBash) Execute(ctx context.Context, arguments string) (*tool.Result, error) {
	args, err := tool.ParseArgs[commands.BashArgs](arguments)
	if err != nil {
		return nil, err
	}
	options := commands.BashExecOptions{OnOutput: operation.InvocationFromContext(ctx).Progress}
	if args.TimeoutSpecified() {
		options.Timeout = time.Duration(args.Timeout) * time.Second
		options.TimeoutSet = true
	}
	return b.RunForegroundTool(ctx, args.Command, options)
}
func (b *recordingBash) RunForegroundTool(_ context.Context, command string, options commands.BashExecOptions) (*tool.Result, error) {
	b.command = command
	b.options = options
	options.OnOutput([]byte("streamed\n"))
	result := tool.TextResult("streamed")
	return result, nil
}

func TestExecuteToolRequestForeground(t *testing.T) {
	bash := &recordingBash{}
	registry := testRegistry(t, bash)
	progressBus := eventbus.New[*toolpb.Progress]()
	var progress []*toolpb.Progress
	progressBus.Subscribe(func(event *toolpb.Progress) {
		progress = append(progress, event)
	})
	event, err := ExecuteToolRequest(context.Background(), "task-1", toolRequest(t, "task-1", "bash", map[string]any{"command": "echo test", "timeout": 7}), registry, progressBus)
	if err != nil {
		t.Fatal(err)
	}
	if bash.command != "echo test" || bash.options.Timeout != 7*time.Second {
		t.Fatalf("bash options = %+v", bash.options)
	}
	if len(progress) != 1 || progress[0].Text != "streamed" || progress[0].CallId != "task-1" {
		t.Fatalf("progress = %+v", progress)
	}
	result := event.GetToolResult()
	if result.IsError || result.Output[0].GetText().Text != "streamed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecuteToolRequestForegroundPreservesExplicitZeroTimeout(t *testing.T) {
	bash := &recordingBash{}
	registry := testRegistry(t, bash)

	_, err := ExecuteToolRequest(context.Background(), "task-zero-timeout", toolRequest(t, "task-zero-timeout", "bash", map[string]any{
		"command": "echo test",
		"timeout": 0,
	}), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bash.options.TimeoutSet || bash.options.Timeout != 0 {
		t.Fatalf("bash options = %+v, want explicit unlimited timeout", bash.options)
	}
}

func TestProgressStreamerSanitizesInvalidUTF8(t *testing.T) {
	progressBus := eventbus.New[*toolpb.Progress]()
	var progress []*toolpb.Progress
	progressBus.Subscribe(func(event *toolpb.Progress) {
		progress = append(progress, event)
	})
	stream := progressStreamer{bus: progressBus, tool: "bash", callID: "task-invalid"}
	stream.Write([]byte{'o', 'k', 0xff, '\n'})
	stream.Write([]byte{0xe4, 0xbd})
	stream.Write([]byte{0xa0, '\n'})
	stream.Flush()

	if len(progress) != 2 {
		t.Fatalf("progress count = %d", len(progress))
	}
	if progress[0].Text != "ok\uFFFD" || progress[1].Text != "\u4f60" {
		t.Fatalf("progress = %#v", progress)
	}
	for _, item := range progress {
		if !utf8.ValidString(item.Text) {
			t.Fatalf("progress is not valid UTF-8: %q", item.Text)
		}
		message := &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: item}}
		if _, err := aop.Wrap("progress", item.CallId, message); err != nil {
			t.Fatalf("wrap progress: %v", err)
		}
	}
}

type invalidTextResultExecutor struct{}

func (invalidTextResultExecutor) Execute(context.Context, string) (*tool.Result, error) {
	invalid := string([]byte{'r', 0xff, 's'})
	return &tool.Result{Output: []*aop.Content{{
		Value: &aop.Content_Text{Text: &aop.TextContent{Text: invalid}},
	}}}, nil
}

func TestExecuteToolRequestSanitizesDirectToolResultText(t *testing.T) {
	event, err := ExecuteToolRequest(context.Background(), "call-invalid", toolRequest(t, "call-invalid", "scan", nil), testRegistry(t, invalidTextResultExecutor{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := event.GetToolResult().GetOutput()[0].GetText().GetText()
	if text != "r\uFFFDs" || !utf8.ValidString(text) {
		t.Fatalf("result text = %q", text)
	}
	message := &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}
	if _, err := aop.Wrap("event", "call-invalid", message); err != nil {
		t.Fatalf("wrap result event: %v", err)
	}
}

type panicForegroundBash struct{ recordingBash }

func (*panicForegroundBash) Execute(context.Context, string) (*tool.Result, error) {
	panic("foreground boom")
}

func (*panicForegroundBash) RunForegroundTool(context.Context, string, commands.BashExecOptions) (*tool.Result, error) {
	panic("foreground boom")
}

func TestExecuteToolRequestForegroundPanicIsReturnedWithoutStack(t *testing.T) {
	registry := testRegistry(t, &panicForegroundBash{})

	event, err := ExecuteToolRequest(context.Background(), "task-panic", toolRequest(t, "task-panic", "bash", map[string]any{"command": "echo test"}), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := event.GetToolResult()
	text := tool.ResultText(result)
	if !result.IsError || result.CallId != "task-panic" || !strings.Contains(text, "unexpectedly") {
		t.Fatalf("result = %+v", result)
	}
	if strings.Contains(text, "foreground boom") || strings.Contains(text, "goroutine") {
		t.Fatalf("tool result leaked panic details: %q", text)
	}
}

func (aopTestExecutor) Name() string                 { return "echo" }
func (aopTestExecutor) Description() string          { return "test" }
func (aopTestExecutor) Definition() *tool.Definition { return tool.Def("echo", "test", struct{}{}) }

func (structuredResultExecutor) Name() string        { return "scan" }
func (structuredResultExecutor) Description() string { return "test" }
func (structuredResultExecutor) Definition() *tool.Definition {
	return tool.Def("scan", "test", struct{}{})
}

func (invalidTextResultExecutor) Name() string        { return "scan" }
func (invalidTextResultExecutor) Description() string { return "test" }
func (invalidTextResultExecutor) Definition() *tool.Definition {
	return tool.Def("scan", "test", struct{}{})
}

func testRegistry(t testing.TB, value tool.Tool) tool.Executor {
	t.Helper()
	return singleToolExecutor{value: value}
}

type singleToolExecutor struct{ value tool.Tool }

func (e singleToolExecutor) ToolDefinitions() []*tool.Definition {
	return []*tool.Definition{e.value.Definition()}
}

func (e singleToolExecutor) ExecuteTool(ctx context.Context, name, arguments string) (*tool.Result, error) {
	if name != e.value.Name() {
		return nil, errors.New("unknown tool")
	}
	return toolhooks.Execute(ctx, nil, name, arguments, e.value.Execute)
}
