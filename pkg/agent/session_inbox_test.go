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

func TestSessionCompletionInjectedIntoAgentLoop(t *testing.T) {
	tools := command.NewRegistry()
	tools.RegisterTool(&recordingTool{name: "echo", output: "tool output"})

	ib := inbox.NewBuffered(8)
	sessMgr := tmux.NewManager()
	sessMgr.SetOnDone(func(info tmux.Info) {
		tail := sessMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{"session_id": info.ID}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := sessMgr.Create(dir, "echo background-result", "bg-scan", 10*time.Second, nil, "")
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
			chatResponse(provider.NewTextMessage("assistant", "saw the background session")),
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
	if result.Output != "saw the background session" {
		t.Fatalf("result = %q, want 'saw the background session'", result.Output)
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
				t.Errorf("session completion should contain stdout, got: %s", *m.Content)
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
		t.Fatalf("turn 2 missing session_completion message.\nMessages:\n%s", strings.Join(contents, "\n---\n"))
	}
}

func TestSessionCompletionMetadata(t *testing.T) {
	ib := inbox.NewBuffered(4)
	sessMgr := tmux.NewManager()
	sessMgr.SetOnDone(func(info tmux.Info) {
		tail := sessMgr.PeekOrEmpty(info.ID, 20)
		msg := inbox.NewMessage(inbox.OriginSession, "user",
			tmux.FormatCompletion(info, tail))
		msg.Meta = map[string]any{
			"session_id":   info.ID,
			"session_name": info.Name,
			"exit_code":    info.ExitCode,
		}
		ib.Push(msg)
	})

	dir := t.TempDir()
	_, err := sessMgr.Create(dir, "echo done", "test-session", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	received := ib.Drain()
	if len(received) == 0 {
		t.Fatal("expected at least 1 inbox message from session completion")
	}

	msg := received[0]
	if msg.Origin != inbox.OriginSession {
		t.Errorf("origin = %q, want %q", msg.Origin, inbox.OriginSession)
	}
	if msg.Meta["session_name"] != "test-session" {
		t.Errorf("session_name = %v, want test-session", msg.Meta["session_name"])
	}
	if msg.Meta["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", msg.Meta["exit_code"])
	}

	cms := msg.ToChatMessages()
	if len(cms) != 1 {
		t.Fatalf("expected 1 chat message, got %d", len(cms))
	}
	if !strings.Contains(*cms[0].Content, "session_completion") {
		t.Errorf("chat message should contain session_completion XML, got: %s", *cms[0].Content)
	}
}
