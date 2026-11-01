package inbox_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	coretool "github.com/chainreactors/cyber/core/tool"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
)

// Exercise a real repository CI continuation: the first Go test command has
// completed, a monitor adds another required suite, and the in-flight model
// exchange fails before consuming that update. No business result is stubbed;
// the provider only selects real commands and injects the transport failure.
func TestRepositoryCIContinuationAfterProviderFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("runs actual repository Go tests through the worker")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("requires Go toolchain")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if parent := os.Getenv("CYBER_INBOX_CASES_OUTPUT"); parent != "" {
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		dir, err = os.MkdirTemp(parent, "repository-ci-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("repository CI evidence: %s", dir)
	quote := func(path string) string {
		return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\"'\"'") + "'"
	}
	command := func(pkg, name, artifact string) *provider.ChatCompletionResponse {
		return toolResponse("bash", map[string]any{"command": fmt.Sprintf("cd %s && go test -json -count=1 -run '^%s$' %s > %s && cat %s", quote(root), name, pkg, quote(filepath.Join(dir, artifact)), quote(filepath.Join(dir, artifact))), "timeout": 25})
	}
	blocked := make(chan string, 1)
	failRequest := make(chan struct{})
	feedback := make(chan mailbox.Message, 8)
	var mailboxCommands, sessionCommands, notesCommands atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if strings.Contains(lastInput(r), "independent release-notes audit") {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("release-notes audit ready"), nil
			}
			notesCommands.Add(1)
			return toolResponse("bash", map[string]any{"command": fmt.Sprintf("cd %s && git log -1 --format='%%H%%n%%s' > %s && cat %s", quote(root), quote(filepath.Join(dir, "revision.txt")), quote(filepath.Join(dir, "revision.txt"))), "timeout": 10}), nil
		}
		followup := false
		mailboxPassed, sessionPassed := false, false
		for _, m := range r.Messages {
			followup = followup || m.Role == "user" && strings.Contains(provider.MessageText(m), "also require the Session delivery suite")
			for _, c := range m.Content {
				if result := c.GetToolResult(); result != nil && result.Name == "bash" {
					if result.IsError {
						return nil, fmt.Errorf("actual repository command failed: %s", coretool.ResultText(result))
					}
					text := coretool.ResultText(result)
					mailboxPassed = mailboxPassed || strings.Contains(text, `"Action":"pass","Package":"github.com/chainreactors/cyber/agent/inbox","Test":"TestInterruptAdmissionAndDrain"`)
					sessionPassed = sessionPassed || strings.Contains(text, `"Action":"pass","Package":"github.com/chainreactors/cyber/agent/session","Test":"TestDeliveryGatesRoutingAndCapacity"`)
				}
			}
		}
		if !mailboxPassed {
			mailboxCommands.Add(1)
			return command("./agent/inbox", "TestInterruptAdmissionAndDrain", "mailbox.jsonl"), nil
		}
		if !followup {
			blocked <- r.SessionID
			select {
			case <-failRequest:
				return nil, errors.New("injected model connection failure after CI output")
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		if !sessionPassed {
			sessionCommands.Add(1)
			return command("./agent/session", "TestDeliveryGatesRoutingAndCapacity", "session.jsonl"), nil
		}
		return response("CI evidence ready: mailbox and Session delivery suites passed"), nil
	})
	config := inboxext.Config{StateDir: filepath.Join(dir, "state"), MaxActive: 2, Notify: func(_ context.Context, m mailbox.Message) error { feedback <- m; return nil }}
	_, router, _, set := activeHost(t, p, config)
	defer func() { closeActive(t, set) }()
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("Release CI: run the actual mailbox interrupt test and retain the Go test evidence."))
	first := waitFor(t, ctx, blocked)
	notes := activeSubmit(t, ctx, router, mailbox.NewUserMessage("Run an independent release-notes audit: record the current repository revision and commit subject while CI is waiting."))
	if result := waitFor(t, ctx, feedback); result.Meta["task_id"] != notes || result.Meta["status"] != "completed" || notes == task {
		t.Fatalf("unrelated release-notes audit blocked behind CI: %v", result.Meta)
	}
	update := mailbox.NewMessage(mailbox.OriginPeer, "user", "CI monitor scope update: also require the Session delivery suite before declaring this release check complete.")
	update.Message.Id = "ci-scope-update"
	update.Meta = map[string]any{"task_id": task, "sender": "ci-monitor", "source_session_id": "release-check", "feedback_chain": []string{"unconsumed-scope-origin"}}
	activeSubmit(t, ctx, router, update)
	close(failRequest)
	failed := waitFor(t, ctx, feedback)
	if failed.Meta["task_id"] != task || failed.Meta["status"] != "failed" {
		t.Fatalf("transport fault was not exercised: %v", failed.Meta)
	}
	if strings.Contains(fmt.Sprint(failed.Meta["feedback_chain"]), "unconsumed-scope-origin") {
		t.Fatal("failed CI feedback claimed ancestry from an unread scope update")
	}
	deadline, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	select {
	case result := <-feedback:
		if result.Meta["task_id"] != task || result.Meta["status"] != "completed" || result.Meta["execution_id"] == first {
			t.Fatalf("CI scope update resumed incorrectly: %v", result.Meta)
		}
		if !strings.Contains(fmt.Sprint(result.Meta["feedback_chain"]), "unconsumed-scope-origin") {
			t.Fatal("resumed CI feedback lost the processed scope update ancestry")
		}
	case <-deadline.Done():
		db, err := sql.Open("sqlite", filepath.Join(dir, "state", "active.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var state string
		var done int
		_ = db.QueryRow("SELECT state FROM tasks WHERE id=?", task).Scan(&state)
		_ = db.QueryRow("SELECT count(*) FROM inputs WHERE task_id=? AND state='done'", task).Scan(&done)
		t.Fatalf("accepted CI scope update never executed after model failure: task=%s acknowledged_inputs=%d", state, done)
	}
	for artifact, testName := range map[string]string{"mailbox.jsonl": "TestInterruptAdmissionAndDrain", "session.jsonl": "TestDeliveryGatesRoutingAndCapacity"} {
		f, err := os.Open(filepath.Join(dir, artifact))
		if err != nil {
			t.Fatal(err)
		}
		scan := bufio.NewScanner(f)
		tests, packages := 0, 0
		for scan.Scan() {
			var event struct{ Action, Test string }
			if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Action == "fail" {
				t.Fatalf("repository suite actually failed: %s", scan.Text())
			}
			if event.Action == "pass" && event.Test == testName {
				tests++
			}
			if event.Action == "pass" && event.Test == "" {
				packages++
			}
		}
		err = errors.Join(scan.Err(), f.Close())
		if err != nil || tests != 1 || packages != 1 {
			t.Fatalf("missing real CI evidence in %s: test_passes=%d package_passes=%d err=%v", artifact, tests, packages, err)
		}
	}
	revision, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	notesEvidence, err := os.ReadFile(filepath.Join(dir, "revision.txt"))
	if err != nil || !strings.HasPrefix(string(notesEvidence), strings.TrimSpace(string(revision))+"\n") {
		t.Fatalf("release-notes audit did not read the current revision: %q err=%v", notesEvidence, err)
	}
	closeActive(t, set)
	_, router, _, set = activeHost(t, p, config)
	if got := activeSubmit(t, ctx, router, update); got != task {
		t.Fatalf("replayed monitor update changed task: %s", got)
	}
	if mailboxCommands.Load() != 1 || sessionCommands.Load() != 1 || notesCommands.Load() != 1 {
		t.Fatalf("CI repeated completed work: mailbox=%d session=%d notes=%d", mailboxCommands.Load(), sessionCommands.Load(), notesCommands.Load())
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "state", "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var attempts, finished, tasks int
	if err := db.QueryRow("SELECT count(*) FROM attempts WHERE task_id=?", task).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM inputs WHERE task_id=? AND state='done'", task).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || finished != 2 || tasks != 2 {
		t.Fatalf("CI completion or replay mismatch: attempts=%d done_inputs=%d tasks=%d", attempts, finished, tasks)
	}
	summary, err := json.MarshalIndent(map[string]any{"task_id": task, "notes_task_id": notes, "first_execution_id": first, "ci_attempts": attempts, "done_ci_inputs": finished, "tasks": tasks, "mailbox_commands": mailboxCommands.Load(), "session_commands": sessionCommands.Load(), "notes_commands": notesCommands.Load(), "revision": strings.TrimSpace(string(revision))}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case-summary.json"), append(summary, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
