package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

// Exercise the actual CLI/config/provider/extension graph in a separate process.
// The HTTP model is scripted; filesystem effects, SQLite and tmux are real.
func TestInboxCLIProcessWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI subprocess integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	dir := t.TempDir()
	config := filepath.Join(dir, "cyber.yaml")
	if err := os.WriteFile(config, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	ioa := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "cli-e2e")))
	defer ioa.Close()
	endpoint := strings.Replace(ioa.URL, "http://", "http://cli-e2e@", 1)
	monitor, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.EnsureRegistered(ctx, "cli-release-monitor", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := monitor.Space(ctx, "cli-release-review", "external release checks")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	failures := make(chan error, 32)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "scenario"}}})
			return
		}
		callID := calls.Add(1)
		var request struct {
			Stream bool `json:"stream"`
			Tools  []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) == 0 {
			failures <- fmt.Errorf("invalid model request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		last := request.Messages[len(request.Messages)-1]
		message := map[string]any{"role": "assistant", "content": "completed"}
		finish := "stop"
		toolName := ""
		var args any
		isRouter := len(request.Tools) == 1 && request.Tools[0].Function.Name == "inbox_route"
		if isRouter && last.Role != "tool" {
			toolName = "inbox_route"
			args = map[string]any{"new_task": map[string]any{}}
			if strings.Contains(string(last.Content), `\"kind\":\"subagent_feedback\"`) {
				args = map[string]any{"action": "reply"}
			}
		} else if !isRouter && len(request.Tools) > 0 {
			var task string
			for _, m := range request.Messages {
				if m.Role != "user" {
					continue
				}
				for _, name := range []string{"alpha", "beta", "gamma", "delta"} {
					if strings.Contains(string(m.Content), "e2e-"+name) {
						if task != "" && task != name {
							failures <- fmt.Errorf("worker history leaked: %s and %s", task, name)
						}
						task = name
					}
				}
			}
			if task == "" {
				failures <- fmt.Errorf("worker received no task")
				http.Error(w, "missing task", http.StatusBadRequest)
				return
			}
			if last.Role != "tool" {
				path := strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, task+".txt")), "'", "'\"'\"'")
				toolName = "bash"
				args = map[string]any{"command": "printf '" + task + "\\n' >> '" + path + "'"}
			}
		}
		if toolName != "" {
			encoded, _ := json.Marshal(args)
			message["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d", callID), "type": "function", "function": map[string]any{"name": toolName, "arguments": string(encoded)}}}
			message["content"] = ""
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
		}
	}))
	defer model.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(name, timeout string) (io.WriteCloser, string, <-chan error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInboxCLIProcessChild$", "-test.timeout=40s", "--",
			"--config", config, "--data-dir", filepath.Join(dir, "data"), "--no-color", "inbox",
			"--base-url", model.URL+"/v1", "--provider", "openai", "--model", "scenario", "--api-key", "local-test-key",
			"--state-dir", filepath.Join(dir, "state"), "--max-active", "2", "--timeout", timeout,
			"--ioa-url", endpoint, "--space", "cli-release-review", "--node-name", "cli-inbox-worker")
		cmd.Dir = dir
		for _, value := range os.Environ() {
			key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
			if !strings.HasPrefix(key, "CYBER_") && !strings.HasPrefix(key, "OPENAI_") && !strings.HasPrefix(key, "ANTHROPIC_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "CYBER_INBOX_CLI_CHILD=1")
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name+".log")
		output, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			_ = output.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { err := cmd.Wait(); _ = output.Close(); done <- err }()
		t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill() })
		return input, path, done
	}
	wait := func(done <-chan error, path string, condition func(string) bool) string {
		t.Helper()
		for {
			b, _ := os.ReadFile(path)
			if condition(string(b)) {
				return string(b)
			}
			select {
			case err := <-done:
				t.Fatalf("CLI exited before expected state: %v\n%s", err, b)
			case err := <-failures:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatalf("CLI scenario timed out: %v\n%s", ctx.Err(), b)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	input, log, done := start("first", "30")
	wait(done, log, func(s string) bool { return strings.Contains(s, "Inbox router listening") })
	db, err := sql.Open("sqlite", filepath.Join(dir, "state", "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	waitCompleted := func(count int) {
		t.Helper()
		wait(done, log, func(string) bool {
			var n int
			return db.QueryRow("SELECT count(*) FROM outbox WHERE state='sent'").Scan(&n) == nil && n == count
		})
	}
	if _, err := io.WriteString(input, "e2e-alpha\ne2e-beta\n!tmux ls\n"); err != nil {
		t.Fatal(err)
	}
	waitCompleted(2)
	listing := wait(done, log, func(s string) bool { return strings.Contains(s, "(router): inbox router") })
	router := regexp.MustCompile(`([^\s]+) \(router\): inbox router`).FindStringSubmatch(listing)
	if len(router) != 2 {
		t.Fatalf("missing router in tmux output: %s", listing)
	}
	if _, err := fmt.Fprintf(input, "!tmux send-keys -t %s e2e-gamma Enter\n", router[1]); err != nil {
		t.Fatal(err)
	}
	waitCompleted(3)
	if _, err := fmt.Fprintf(input, "!tmux capture-pane -t %s --full\n", router[1]); err != nil {
		t.Fatal(err)
	}
	wait(done, log, func(s string) bool { return strings.Contains(s, "routed") })
	nodes, err := monitor.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var receiver string
	for _, node := range nodes {
		if node.Name == "cli-inbox-worker" {
			receiver = node.ID
		}
	}
	if receiver == "" {
		t.Fatal("CLI inbox did not register its IOA identity")
	}
	request, err := monitor.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": "e2e-delta"}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: map[string]any{"source_session_id": "release-monitor"}})
	if err != nil {
		t.Fatal(err)
	}
	waitCompleted(4)
	transcript, err := monitor.ReadPublic(ctx, space.ID, protocols.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, message := range transcript {
		if message.Meta["kind"] != "task_result" {
			continue
		}
		results++
		chain, ok := message.Meta["feedback_chain"].([]any)
		var execution string
		if err := db.QueryRow("SELECT session_id FROM tasks WHERE id=?", message.Meta["source_task_id"]).Scan(&execution); err != nil {
			t.Fatal(err)
		}
		if !ok || len(chain) != 1 || chain[0] != message.Meta["source_task_id"] || message.Meta["source_session_id"] != execution || message.Meta["target_session_id"] != "release-monitor" || message.Meta["feedback_id"] != "feedback-"+execution {
			t.Fatalf("CLI result lost execution identity or feedback ancestry: %v", message.Meta)
		}
		if len(message.Refs.Nodes) != 1 || message.Refs.Nodes[0] != monitor.NodeID() || len(message.Refs.Messages) != 1 || message.Refs.Messages[0] != request.ID {
			t.Fatalf("CLI result lost its requester: %+v", message.Refs)
		}
	}
	if results != 1 {
		t.Fatalf("external check received %d results, want one", results)
	}
	secondInput, secondLog, secondDone := start("competing", "5")
	_ = secondInput.Close()
	select {
	case err := <-secondDone:
		b, _ := os.ReadFile(secondLog)
		if err == nil || !strings.Contains(string(b), "already owned") {
			t.Fatalf("competing host: %v\n%s", err, b)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := io.WriteString(input, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			b, _ := os.ReadFile(log)
			t.Fatalf("shutdown: %v\n%s", err, b)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	input, log, done = start("restarted", "8")
	_ = input.Close()
	wait(done, log, func(s string) bool { return strings.Contains(s, "Inbox router listening") })
	before := calls.Load()
	select {
	case err := <-done:
		t.Fatalf("EOF stopped inbox: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	select {
	case err := <-done:
		if err != nil {
			b, _ := os.ReadFile(log)
			t.Fatalf("timeout shutdown: %v\n%s", err, b)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := calls.Load(); got != before {
		t.Fatalf("idle restart called model: before=%d after=%d", before, got)
	}
	for _, name := range []string{"alpha", "beta", "gamma", "delta"} {
		b, err := os.ReadFile(filepath.Join(dir, name+".txt"))
		if err != nil || string(b) != name+"\n" {
			t.Fatalf("file side effect %s duplicated/lost: %q %v", name, b, err)
		}
	}
	var tasks, attempts int
	if err := db.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if tasks != 4 || attempts != 4 {
		t.Fatalf("unexpected task/execution counts: %d/%d", tasks, attempts)
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	t.Logf("4 isolated real shell tasks; external IOA request and result ancestry; tmux list/input/capture; exclusive host lock; clean exit; EOF persistence; restart without replay (%d HTTP model calls)", calls.Load())
}

func TestInboxCLIProcessChild(t *testing.T) {
	if os.Getenv("CYBER_INBOX_CLI_CHILD") != "1" {
		t.Skip("subprocess only")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := runCLI(t.Context(), os.Args[i+1:], os.Stdin, os.Stdout, os.Stderr, nil); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing CLI arguments")
}
