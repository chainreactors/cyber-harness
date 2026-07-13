package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/tui/readline/inputrc"
)

type captureConsoleProvider struct {
	requests []*agent.ChatCompletionRequest
}

func (p *captureConsoleProvider) Name() string { return "capture" }

func (p *captureConsoleProvider) ChatCompletion(_ context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	cp := *req
	cp.Messages = append([]agent.ChatMessage(nil), req.Messages...)
	p.requests = append(p.requests, &cp)
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{
			Message: agent.NewTextMessage("assistant", "ok"),
		}},
	}, nil
}

func TestAgentConsoleArgsForLineBangCommand(t *testing.T) {
	got, err := AgentConsoleArgsForLine("!echo chat_pass")
	if err != nil {
		t.Fatalf("AgentConsoleArgsForLine returned error: %v", err)
	}
	want := []string{"!", "echo chat_pass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentConsoleArgsForLine = %#v, want %#v", got, want)
	}
}

func TestAgentReadlineBackspaceBindings(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	if !shell.Config.GetBool("menu-complete-display-prefix") {
		t.Fatal("menu-complete-display-prefix should stay enabled so completion replaces the typed prefix")
	}
	if shell.Config.GetBool("autocomplete-select") {
		t.Fatal("autocomplete-select should stay disabled so typing does not hijack arrow keys before Tab")
	}
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert"} {
		for _, seq := range []string{inputrc.Unescape(`\C-h`), inputrc.Unescape(`\C-?`)} {
			bind, ok := shell.Config.Binds[keymap][seq]
			if !ok {
				t.Fatalf("%s missing bind for %q", keymap, inputrc.Escape(seq))
			}
			if bind.Action != "backward-delete-char" {
				t.Fatalf("%s %q action = %q", keymap, inputrc.Escape(seq), bind.Action)
			}
		}
		tabBind, ok := shell.Config.Binds[keymap][`\t`]
		if !ok {
			t.Fatalf("%s missing bind for tab", keymap)
		}
		if tabBind.Action != "menu-complete" {
			t.Fatalf("%s tab action = %q, want menu-complete", keymap, tabBind.Action)
		}
	}
}

func TestAgentReadlinePendingBracketedPaste(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	if !shell.HandleBracketedPastePending("[200~demo_reqresp\x1b[201~") {
		t.Fatal("pending bracketed paste was not handled")
	}
	if got := string(*shell.Line()); got != "demo_reqresp" {
		t.Fatalf("single-line paste = %q", got)
	}
}

func TestAgentReadlinePendingMultilinePasteReference(t *testing.T) {
	repl := NewAgentConsole(context.Background(), &cfg.Option{}, AppInfo{}, nil, nil)
	shell := repl.console.Shell()
	if !shell.HandleBracketedPastePending("[200~alpha\nbeta\x1b[201~") {
		t.Fatal("pending bracketed paste was not handled")
	}
	const placeholder = "[Pasted text #1 +2 lines]"
	if got := string(*shell.Line()); got != placeholder {
		t.Fatalf("multiline paste = %q", got)
	}
	_, resolved := repl.resolvePastedText(placeholder)
	if resolved != "alpha\nbeta" {
		t.Fatalf("resolved paste = %q", resolved)
	}
}

func TestFuzzySubsequenceMatching(t *testing.T) {
	tests := []struct {
		query, value string
		want         bool
	}{
		{"af", "abcdef", true},
		{"abc", "abcdef", true},
		{"adf", "abcdef", true},
		{"xyz", "abcdef", false},
		{"AF", "abcdef", true},
		{"", "anything", true},
	}
	for _, tt := range tests {
		if got := fuzzySubsequence(tt.query, tt.value); got != tt.want {
			t.Errorf("fuzzySubsequence(%q, %q) = %v, want %v", tt.query, tt.value, got, tt.want)
		}
	}
}

func TestSplitCompletionPath(t *testing.T) {
	dir, query, _ := splitCompletionPath("src/ma")
	if dir != "src/" || query != "ma" {
		t.Fatalf("splitCompletionPath(\"src/ma\") = %q, %q", dir, query)
	}
	dir, query, _ = splitCompletionPath("ab")
	if dir != "" || query != "ab" {
		t.Fatalf("splitCompletionPath(\"ab\") = %q, %q", dir, query)
	}
}

func TestReadlineDoesNotSuppressLiveStatusWhileTaskRuns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	repl := NewAgentConsoleWithWriters(context.Background(), &cfg.Option{}, AppInfo{}, agent.NewAgent(agent.Config{}), &stdout, &stderr)
	repl.controller.mu.Lock()
	repl.controller.running = true
	repl.controller.mu.Unlock()

	repl.setReadlineActive(true)

	repl.output.mu.Lock()
	active := repl.output.interactiveInputActive
	repl.output.mu.Unlock()
	if active {
		t.Fatal("running task should keep live status enabled")
	}
}

func TestAgentConsoleCtrlCWarnsAndClearsInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	repl := NewAgentConsoleWithWriters(context.Background(), &cfg.Option{}, AppInfo{}, nil, &stdout, &stderr)
	repl.console.Shell().Line().Set([]rune("exit")...)

	repl.handleCtrlC()

	if !repl.pendingExit.Load() {
		t.Fatal("pending exit was not set")
	}
	if got := string(*repl.console.Shell().Line()); got != "" {
		t.Fatalf("input line = %q, want empty", got)
	}
	out := stdout.String() + stderr.String()
	if !strings.Contains(out, "Press Ctrl+C again to exit") {
		t.Fatalf("missing Ctrl+C hint:\n%s", out)
	}
	if strings.Contains(stripANSI(out), "aiscan> exit") {
		t.Fatalf("Ctrl+C leaked input as output:\n%s", out)
	}
}

func TestAgentConsoleModelCommandListsAndSwitches(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "model-a"},
				{"id": "model-b"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	option := &cfg.Option{}
	session := agent.NewAgent(agent.Config{Model: "model-a"})
	var changed agent.ProviderConfig
	repl := NewAgentConsoleWithWriters(context.Background(), option, AppInfo{
		ProviderConfig: agent.ProviderConfig{
			Provider: "openai",
			BaseURL:  srv.URL + "/v1",
			APIKey:   "sk-test",
			Model:    "model-a",
		},
		OnProviderChange: func(_ agent.Provider, providerConfig agent.ProviderConfig) {
			changed = providerConfig
		},
	}, session, &stdout, &stderr)

	if _, err := repl.ExecuteLineAndWait("/model"); err != nil {
		t.Fatalf("/model: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "model-a  active") || !strings.Contains(out, "model-b") {
		t.Fatalf("/model output missing models:\n%s", out)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := repl.ExecuteLineAndWait("/model 2"); err != nil {
		t.Fatalf("/model 2: %v\nstderr=%s", err, stderr.String())
	}
	if changed.Model != "model-b" {
		t.Fatalf("changed model = %q, want model-b", changed.Model)
	}
	if option.Model != "model-b" {
		t.Fatalf("option model = %q, want model-b", option.Model)
	}
	if session.Cfg.Model != "model-b" {
		t.Fatalf("session model = %q, want model-b", session.Cfg.Model)
	}
	if out := stdout.String(); !strings.Contains(out, "Model ready: openai / model-b") {
		t.Fatalf("switch output = %q", out)
	}
}

func TestAgentConsoleResumeLoadsSessionMessages(t *testing.T) {
	dir := t.TempDir()
	if err := agent.SaveSession(dir, &agent.SessionData{
		Model:    "test-model",
		Provider: "capture",
		Messages: []agent.ChatMessage{
			agent.NewTextMessage("user", "previous user"),
			agent.NewTextMessage("assistant", "previous assistant"),
		},
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	sessions, err := agent.ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}
	path := sessions[0].Path

	var stdout, stderr bytes.Buffer
	prov := &captureConsoleProvider{}
	session := agent.NewAgent(agent.Config{Provider: prov, Model: "test-model"})
	repl := NewAgentConsoleWithWriters(context.Background(), &cfg.Option{}, AppInfo{}, session, &stdout, &stderr)

	if _, err := repl.ExecuteLineAndWait("/resume " + path); err != nil {
		t.Fatalf("/resume: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Resumed 2 messages") {
		t.Fatalf("resume output = %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := repl.ExecuteLineAndWait("new prompt"); err != nil {
		t.Fatalf("prompt after resume: %v\nstderr=%s", err, stderr.String())
	}
	if len(prov.requests) == 0 {
		t.Fatal("provider was not called")
	}
	var contents []string
	for _, msg := range prov.requests[0].Messages {
		if msg.Content != nil {
			contents = append(contents, *msg.Content)
		}
	}
	joined := strings.Join(contents, "\n")
	for _, want := range []string{"previous user", "previous assistant", "new prompt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("request messages missing %q:\n%s", want, joined)
		}
	}
}

func TestAgentConsoleResumeListsAndSelectsSession(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "session-old.json")
	newPath := filepath.Join(dir, "session-new.json")
	writeConsoleSession(t, oldPath, "old-model", "old message", time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC))
	writeConsoleSession(t, newPath, "new-model", "new message", time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))

	var stdout, stderr bytes.Buffer
	session := agent.NewAgent(agent.Config{})
	repl := NewAgentConsoleWithWriters(context.Background(), &cfg.Option{}, AppInfo{}, session, &stdout, &stderr)
	repl.sessionDir = dir

	if _, err := repl.ExecuteLineAndWait("/resume list"); err != nil {
		t.Fatalf("/resume list: %v\nstderr=%s", err, stderr.String())
	}
	listOut := stdout.String()
	if !strings.Contains(listOut, "session-new.json") || !strings.Contains(listOut, "session-old.json") {
		t.Fatalf("resume list missing sessions:\n%s", listOut)
	}
	if strings.Index(listOut, "session-new.json") > strings.Index(listOut, "session-old.json") {
		t.Fatalf("sessions not sorted newest first:\n%s", listOut)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := repl.ExecuteLineAndWait("/resume 1"); err != nil {
		t.Fatalf("/resume 1: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Resumed 1 messages from "+newPath) {
		t.Fatalf("resume output = %q", out)
	}
}

func writeConsoleSession(t *testing.T, path, model, content string, updatedAt time.Time) {
	t.Helper()
	raw, err := json.Marshal(agent.SessionData{
		Version:   1,
		UpdatedAt: updatedAt,
		Model:     model,
		Messages:  []agent.ChatMessage{agent.NewTextMessage("user", content)},
	})
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}
