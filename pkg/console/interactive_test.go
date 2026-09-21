package console

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/output"
	"github.com/chainreactors/tui/readline/inputrc"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestIsLocalAgentTerminal(t *testing.T) {
	local := rlterm.Local()
	if !isLocalAgentTerminal(local) {
		t.Fatal("local terminal should be eligible for native readline rendering")
	}

	var output bytes.Buffer
	remote := rlterm.Stream(bytes.NewReader(nil), &output, &output, rlterm.NewControl(true, 80, 24))
	if isLocalAgentTerminal(remote) {
		t.Fatal("remote terminal must not use local readline rendering")
	}
}

func TestAgentComposerPromptPlacesStatusAboveInput(t *testing.T) {
	bridge := &readlineConsoleBridge{}
	bridge.UpdateStatus("thinking")

	if got, want := agentComposerPrompt(nil, bridge), "thinking\naiscan> "; got != want {
		t.Fatalf("composer prompt = %q, want %q", got, want)
	}
}

func TestAgentConsoleResetsUnsupportedTerminalInputModes(t *testing.T) {
	var output bytes.Buffer
	repl := &AgentConsole{terminal: rlterm.Stream(
		bytes.NewReader(nil),
		&output,
		&output,
		rlterm.NewControl(true, 80, 24),
	)}

	repl.resetTerminalInputModes()
	if got := output.String(); got != agentConsoleResetInputModes {
		t.Fatalf("terminal input-mode reset = %q, want %q", got, agentConsoleResetInputModes)
	}
}

type captureConsoleProvider struct {
	requests []*agent.ChatCompletionRequest
}

func (p *captureConsoleProvider) Name() string { return "capture" }

func (p *captureConsoleProvider) ChatCompletion(_ context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	cp := *req
	cp.Messages = append([]*aop.Message(nil), req.Messages...)
	p.requests = append(p.requests, &cp)
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{
			Message: agent.TextMessage("assistant", "ok"),
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

func TestAgentConsoleBangCommandTerminatesOutputLine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, &stdout, &stderr)
	if _, err := executeAndWait(repl, "!printf DIRECT_OK"); err != nil {
		t.Fatal(err)
	}
	if got := output.StripANSI(stdout.String()); !strings.HasSuffix(got, "DIRECT_OK\n") {
		t.Fatalf("output = %q", got)
	}
}

func TestAgentReadlineBackspaceBindings(t *testing.T) {
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, io.Discard, io.Discard)
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
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, io.Discard, io.Discard)
	shell := repl.console.Shell()
	if !shell.HandleBracketedPastePending("[200~demo_reqresp\x1b[201~") {
		t.Fatal("pending bracketed paste was not handled")
	}
	if got := string(*shell.Line()); got != "demo_reqresp" {
		t.Fatalf("single-line paste = %q", got)
	}
}

func TestAgentReadlinePendingMultilinePasteReference(t *testing.T) {
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, io.Discard, io.Discard)
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
	var stdout, stderr syncedBuffer
	p := &gateProvider{release: make(chan struct{})}
	repl, _ := newTestConsole(t, &cfg.Option{}, p, &stdout, &stderr)
	if err := repl.submitPrompt("hello", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.calls.Load() == 1 }, "provider")
	repl.setReadlineActive(true)
	repl.output.mu.Lock()
	active := repl.output.interactiveInputActive
	repl.output.mu.Unlock()
	if active {
		t.Fatal("running task suppressed live status")
	}
}

func TestAgentConsoleRotatesSessionAfterRuntimeResumeAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	var stdout, stderr bytes.Buffer
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, &stdout, &stderr)
	handle := repl.session
	previousID := handle.ID()
	writeConsoleSession(t, path, "test", time.Now(), agent.TextMessage("user", "history"))
	if _, err := executeAndWait(repl, "/resume "+path); err != nil {
		t.Fatal(err)
	}
	resumedID := handle.ID()
	if previousID == resumedID || repl.session != handle {
		t.Fatal("resume did not rotate through the existing handle")
	}
	if _, err := executeAndWait(repl, "/clear"); err != nil {
		t.Fatal(err)
	}
	if handle.ID() == resumedID || len(handle.MessagesSnapshot()) != 0 {
		t.Fatal("clear did not rotate session")
	}
}

func TestAgentConsoleCtrlCWarnsAndClearsInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, &stdout, &stderr)
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
	repl, _ := newTestConsole(t, option, nil, &stdout, &stderr, agent.ProviderConfig{Provider: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "model-a"})
	session := repl.session

	if _, err := executeAndWait(repl, "/model"); err != nil {
		t.Fatalf("/model: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "model-a  active") || !strings.Contains(out, "model-b") {
		t.Fatalf("/model output missing models:\n%s", out)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := executeAndWait(repl, "/model 2"); err != nil {
		t.Fatalf("/model 2: %v\nstderr=%s", err, stderr.String())
	}
	changed := repl.providerConfig()
	if changed.Model != "model-b" {
		t.Fatalf("changed model = %q, want model-b", changed.Model)
	}
	if option.Model != "" {
		t.Fatalf("session model leaked into startup options: %q", option.Model)
	}
	status, err := session.Command(t.Context(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if text := provider.MessageText(&aop.Message{Content: status.GetContent()}); !strings.Contains(text, "model-b") {
		t.Fatalf("session status = %q, want model-b", text)
	}
	if out := stdout.String(); !strings.Contains(out, "Model ready: openai / model-b") {
		t.Fatalf("switch output = %q", out)
	}
	if _, err := executeAndWait(repl, "/provider set --model global-change"); err == nil {
		t.Fatal("terminal still accepts global provider mutation")
	}
	_, profileConfig := repl.runtime.ProviderState()
	if profileConfig.Model != "model-a" || repl.session.Model() != "model-b" {
		t.Fatal("terminal changed Profile configuration or lost its session model")
	}
}

func TestAgentConsoleResumeLoadsSessionMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-resume.jsonl")
	writeConsoleSession(t, path, "test-model", time.Now(),
		agent.TextMessage("user", "previous user"),
		agent.TextMessage("assistant", "previous assistant"),
	)
	var stdout, stderr bytes.Buffer
	prov := &captureConsoleProvider{}
	repl, _ := newTestConsole(t, &cfg.Option{}, prov, &stdout, &stderr)

	if _, err := executeAndWait(repl, "/resume "+path); err != nil {
		t.Fatalf("/resume: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Resumed 2 messages") {
		t.Fatalf("resume output = %q", out)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := executeAndWait(repl, "new prompt"); err != nil {
		t.Fatalf("prompt after resume: %v\nstderr=%s", err, stderr.String())
	}
	if len(prov.requests) == 0 {
		t.Fatal("provider was not called")
	}
	var contents []string
	for _, msg := range prov.requests[0].Messages {
		contents = append(contents, provider.MessageText(msg))
	}
	joined := strings.Join(contents, "\n")
	for _, want := range []string{"previous user", "previous assistant", "new prompt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("request messages missing %q:\n%s", want, joined)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := executeAndWait(repl, "/clear"); err != nil {
		t.Fatalf("/clear: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Context cleared.") {
		t.Fatalf("clear output = %q", out)
	}
	if messages := repl.session.MessagesSnapshot(); len(messages) != 0 {
		t.Fatalf("messages after clear = %d, want 0", len(messages))
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := executeAndWait(repl, "after clear"); err != nil {
		t.Fatalf("prompt after clear: %v\nstderr=%s", err, stderr.String())
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(prov.requests))
	}
	var afterClear []string
	for _, msg := range prov.requests[1].Messages {
		afterClear = append(afterClear, provider.MessageText(msg))
	}
	afterClearText := strings.Join(afterClear, "\n")
	if !strings.Contains(afterClearText, "after clear") {
		t.Fatalf("request after clear missing new prompt:\n%s", afterClearText)
	}
	for _, stale := range []string{"previous user", "previous assistant", "new prompt"} {
		if strings.Contains(afterClearText, stale) {
			t.Fatalf("request after clear retained %q:\n%s", stale, afterClearText)
		}
	}
}

func TestAgentConsoleResumeListsAndSelectsSession(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "session-old.jsonl")
	newPath := filepath.Join(dir, "session-new.jsonl")
	writeConsoleSession(t, oldPath, "old-model", time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC), agent.TextMessage("user", "old message"))
	writeConsoleSession(t, newPath, "new-model", time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC), agent.TextMessage("user", "new message"))

	var stdout, stderr bytes.Buffer
	repl, _ := newTestConsole(t, &cfg.Option{}, nil, &stdout, &stderr)
	repl.sessionDir = dir

	if _, err := executeAndWait(repl, "/resume list"); err != nil {
		t.Fatalf("/resume list: %v\nstderr=%s", err, stderr.String())
	}
	listOut := stdout.String()
	if !strings.Contains(listOut, "session-new.jsonl") || !strings.Contains(listOut, "session-old.jsonl") {
		t.Fatalf("resume list missing sessions:\n%s", listOut)
	}
	if strings.Index(listOut, "session-new.jsonl") > strings.Index(listOut, "session-old.jsonl") {
		t.Fatalf("sessions not sorted newest first:\n%s", listOut)
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := executeAndWait(repl, "/resume 1"); err != nil {
		t.Fatalf("/resume 1: %v\nstderr=%s", err, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "Resumed 1 messages from "+newPath) {
		t.Fatalf("resume output = %q", out)
	}
}

func writeConsoleSession(t *testing.T, path, model string, updatedAt time.Time, messages ...*aop.Message) {
	t.Helper()
	events := []*aop.Event{{
		Id: "e-1", SessionId: "console-session", Emitter: "cyber", EmittedAt: timestamppb.New(updatedAt),
		Payload: &aop.Event_SessionStarted{SessionStarted: &aop.SessionStarted{Model: model}},
	}}
	if err := types.SetSessionHistory(events[0], &types.SessionHistory{Mode: types.SessionHistory_MODE_INHERIT}); err != nil {
		t.Fatal(err)
	}
	for i, message := range messages {
		message.Id = fmt.Sprintf("m-%d", i+1)
		events = append(events, &aop.Event{
			Id: fmt.Sprintf("e-%d", i+2), SessionId: "console-session", TurnId: "turn-1",
			Emitter: "cyber", EmittedAt: timestamppb.New(updatedAt),
			Payload: &aop.Event_Message{Message: message},
		})
	}
	var lines strings.Builder
	for _, event := range events {
		raw, err := protojson.Marshal(event)
		if err != nil {
			t.Fatalf("marshal session event: %v", err)
		}
		lines.Write(raw)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(lines.String()), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.Chtimes(path, updatedAt, updatedAt); err != nil {
		t.Fatalf("set session time: %v", err)
	}
}

func TestAgentConsoleArgsForLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantArgs []string
	}{
		{name: "empty", input: "  ", wantArgs: nil},
		{name: "prompt", input: " scan localhost ", wantArgs: []string{"__prompt", "scan localhost"}},
		{name: "quoted prompt is preserved", input: `explain "scan result"`, wantArgs: []string{"__prompt", `explain "scan result"`}},
		{name: "help", input: "/help", wantArgs: []string{"/help"}},
		{name: "reset", input: "/reset", wantArgs: []string{"/reset"}},
		{name: "continue", input: "/continue", wantArgs: []string{"/continue"}},
		{name: "resume", input: "/resume 1", wantArgs: []string{"/resume", "1"}},
		{name: "exit", input: "/exit", wantArgs: []string{"/exit"}},
		{name: "quit", input: "/quit", wantArgs: []string{"/quit"}},
		{name: "skill slash command preserves prompt", input: `/scan explain "scan result"`, wantArgs: []string{"/scan", `explain "scan result"`}},
		{name: "unknown slash command", input: "/unknown", wantArgs: []string{"/unknown"}},
		{name: "colon-prefixed unknown command stays prompt", input: "/skill:scan check target", wantArgs: []string{"__prompt", "/skill:scan check target"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, err := AgentConsoleArgsForLine(tt.input)
			if err != nil {
				t.Fatalf("AgentConsoleArgsForLine() error = %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("AgentConsoleArgsForLine() = %#v, want %#v", gotArgs, tt.wantArgs)
			}
		})
	}
}
