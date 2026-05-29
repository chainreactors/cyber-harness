package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/command"
)

func TestTaskCompletionInjectedIntoAgentLoop(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})

	ib := inbox.NewBuffered(8)
	taskMgr := tmux.NewManager()
	taskMgr.SetOnDone(func(info tmux.Info) {
		tail := taskMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginTask, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{"task_id": info.ID}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := taskMgr.Create(dir, "echo background-result", "bg-scan", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	scripted := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: provider.FunctionCall{Name: "echo", Arguments: "{}"},
				}},
			}),
			chatResponse(provider.NewTextMessage("assistant", "saw the background task")),
		},
	}

	result, err := (Config{
		Provider:     scripted,
		Tools:        tools,
		Model:        "test",
		SystemPrompt: "system",
		Inbox:        ib,
	}).Run(context.Background(), "run a scan")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "saw the background task" {
		t.Fatalf("result = %q, want 'saw the background task'", result.Output)
	}

	requests := scripted.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("expected 2 LLM requests, got %d", len(requests))
	}

	turn2Msgs := requests[1].Messages
	found := false
	for _, m := range turn2Msgs {
		if m.Content != nil && strings.Contains(*m.Content, "session_completion") {
			found = true
			if !strings.Contains(*m.Content, "background-result") {
				t.Errorf("task completion should contain stdout, got: %s", *m.Content)
			}
			break
		}
	}
	if !found {
		var contents []string
		for _, m := range turn2Msgs {
			if m.Content != nil {
				contents = append(contents, *m.Content)
			}
		}
		t.Fatalf("turn 2 missing task_completion message.\nMessages:\n%s", strings.Join(contents, "\n---\n"))
	}
}

func TestTaskCompletionMetadata(t *testing.T) {
	ib := inbox.NewBuffered(4)
	taskMgr := tmux.NewManager()
	taskMgr.SetOnDone(func(info tmux.Info) {
		tail := taskMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginTask, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{
			"task_id":   info.ID,
			"task_name": info.Name,
			"exit_code": info.ExitCode,
		}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := taskMgr.Create(dir, "echo done", "test-task", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	received := ib.Drain()
	if len(received) == 0 {
		t.Fatal("expected at least 1 inbox message from task completion")
	}

	msg := received[0]
	if msg.Origin != inbox.OriginTask {
		t.Errorf("origin = %q, want %q", msg.Origin, inbox.OriginTask)
	}
	if msg.Meta["task_name"] != "test-task" {
		t.Errorf("task_name = %v, want test-task", msg.Meta["task_name"])
	}
	if msg.Meta["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", msg.Meta["exit_code"])
	}

	cms := msg.ToChatMessages()
	if len(cms) != 1 {
		t.Fatalf("expected 1 chat message, got %d", len(cms))
	}
	if !strings.Contains(*cms[0].Content, "session_completion") {
		t.Errorf("chat message should contain task_completion XML, got: %s", *cms[0].Content)
	}
}
