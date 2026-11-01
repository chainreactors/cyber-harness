package inbox_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

// A release builder produces a real source digest. A reviewer consumes it over
// HTTP/SSE, then sees an echoed result, an invalid new-work suggestion and a
// too-long feedback chain. Safety must survive the wire and a complete restart,
// even when the routing model tries an unsafe destination before correcting it.
func TestReleaseReviewFeedbackSafetyAcrossIOA(t *testing.T) {
	if testing.Short() {
		t.Skip("repository tools and IOA integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	dir := t.TempDir()
	if parent := os.Getenv("CYBER_INBOX_CASES_OUTPUT"); parent != "" {
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(parent, "release-feedback-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source, err := exec.CommandContext(ctx, "git", "-C", root, "show", "HEAD:go.mod").Output()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	digest := hex.EncodeToString(sum[:])
	quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\"'\"'") + "'" }
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "release-feedback")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://release-feedback@", 1)
	builder, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.EnsureRegistered(ctx, "release-builder", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := builder.Space(ctx, "release-review", "source evidence and reviewer feedback")
	if err != nil {
		t.Fatal(err)
	}
	feedback := make(chan mailbox.Message, 32)
	wires := make(chan mailbox.Message, 32)
	reviewerModel := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				for _, part := range r.Messages[len(r.Messages)-1].Content {
					if result := part.GetToolResult(); result != nil && result.IsError {
						return toolResponse("inbox_route", map[string]any{"action": "record"}), nil
					}
				}
				return response("routing complete"), nil
			}
			text := strings.SplitN(strings.TrimPrefix(lastInput(r), "Current routing context:\n"), "\nOriginal message:\n", 2)[0]
			var routing struct {
				Meta map[string]any `json:"meta"`
			}
			if err := json.Unmarshal([]byte(text), &routing); err != nil {
				return nil, err
			}
			if routing.Meta["kind"] == "subagent_feedback" {
				return toolResponse("inbox_route", map[string]any{"action": "reply"}), nil
			}
			if strings.Contains(lastInput(r), "SUGGEST_NEW_WORK") {
				return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{}}), nil
			}
			if task, _ := routing.Meta["task_id"].(string); task != "" {
				return toolResponse("inbox_route", map[string]any{"task_id": task}), nil
			}
			if execution, _ := routing.Meta["target_session_id"].(string); execution != "" {
				return toolResponse("inbox_route", map[string]any{"session_id": execution}), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"label": "release-reviewer"}}), nil
		}
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, part := range r.Messages[len(r.Messages)-1].Content {
				if result := part.GetToolResult(); result != nil && result.IsError {
					return nil, fmt.Errorf("release audit failed: %s", coretool.ResultText(result))
				}
			}
			return response("review evidence committed"), nil
		}
		if !strings.Contains(lastInput(r), "SOURCE_DIGEST=") {
			return response("reviewer ready"), nil
		}
		marker := strings.Fields(strings.SplitN(lastInput(r), "SOURCE_DIGEST=", 2)[1])[0]
		return toolResponse("bash", map[string]any{"command": fmt.Sprintf("printf '%%s\\n' %s >> %s", quote(marker), quote(filepath.Join(dir, "review-audit.txt"))), "timeout": 10}), nil
	})
	state := filepath.Join(dir, "reviewer-state")
	var receiver string
	open := func() (*inboxext.Router, *extension.Set) {
		f := apptest.NewFixture(t, nil, nil)
		f.Providers.Set(reviewerModel, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
		options, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: state}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": "release-review"}}})
		if err != nil {
			t.Fatal(err)
		}
		connection := ioaclient.New(*options)
		inbox := inboxext.New(inboxext.Config{StateDir: state, Notify: func(_ context.Context, m mailbox.Message) error { feedback <- m; return nil }})
		router := inbox.Router()
		adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: func(callCtx context.Context, m mailbox.Message) error {
			err := router.Deliver(callCtx, m)
			if err == nil {
				wires <- m
			}
			return err
		}, LoadCursor: router.LoadCursor, SaveCursor: router.SaveCursor})
		entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, inbox)
		set := hosttest.Load(t, ctx, entries...)
		if _, err := connection.Service().ReadySpace(ctx); err != nil {
			t.Fatal(err)
		}
		id := connection.Service().Client().NodeID()
		if receiver != "" && receiver != id {
			t.Fatal("reviewer identity changed")
		}
		receiver = id
		return router, set
	}
	router, set := open()
	defer func() { closeActive(t, set) }()
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("Prepare the release evidence reviewer."))
	initial := waitFor(t, ctx, feedback)
	send := func(id, kind, text string, chain []string, target bool) protocols.Message {
		meta := map[string]any{"feedback_id": id, "kind": kind, "source_session_id": "builder-execution", "feedback_chain": chain}
		if target {
			meta["task_id"] = task
		}
		m, err := builder.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": text}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: meta})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	buildModel := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, part := range r.Messages[len(r.Messages)-1].Content {
				if result := part.GetToolResult(); result != nil {
					if result.IsError {
						return nil, fmt.Errorf("build manifest failed: %s", coretool.ResultText(result))
					}
					return response("SOURCE_DIGEST=" + strings.Fields(coretool.ResultText(result))[0]), nil
				}
			}
		}
		return toolResponse("bash", map[string]any{"command": fmt.Sprintf("git -C %s show HEAD:go.mod | sha256sum > %s && cat %s", quote(root), quote(filepath.Join(dir, "source.sha256")), quote(filepath.Join(dir, "source.sha256"))), "timeout": 10}), nil
	})
	_, producer, _, producerSet := activeHost(t, buildModel, inboxext.Config{StateDir: filepath.Join(dir, "builder-state"), Notify: func(callCtx context.Context, m mailbox.Message) error {
		_, err := builder.Send(callCtx, space.ID, protocols.SendMessage{Content: map[string]any{"text": provider.MessageText(m.Message)}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: map[string]any{"task_id": task, "kind": "task_result", "feedback_id": m.Message.Id, "source_session_id": "builder-execution", "feedback_chain": m.Meta["feedback_chain"]}})
		return err
	}})
	defer closeActive(t, producerSet)
	buildTask := activeSubmit(t, ctx, producer, mailbox.NewUserMessage("Compute the release source manifest from the current committed go.mod."))
	first := waitFor(t, ctx, wires)
	if got := activeSubmit(t, ctx, router, first); got != task {
		t.Fatalf("valid release evidence changed task: %s", got)
	}
	result := waitFor(t, ctx, feedback)
	if !strings.Contains(fmt.Sprint(result.Meta["feedback_chain"]), buildTask) || !strings.Contains(fmt.Sprint(result.Meta["feedback_chain"]), task) {
		t.Fatalf("review result lost builder/reviewer ancestry: %v", result.Meta)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "review-audit.txt")); err != nil || string(b) != digest+"\n" {
		t.Fatalf("review did not audit real source digest: %q %v", b, err)
	}
	closeActive(t, set)
	// A feedback echo is retained by IOA while the reviewer is offline.
	send("echoed-review", "task_result", "SOURCE_DIGEST="+digest, []string{buildTask, task}, true)
	router, set = open()
	checkRecorded := func(id string) {
		m := waitFor(t, ctx, wires)
		for m.Meta["feedback_id"] != id {
			m = waitFor(t, ctx, wires)
		}
		r, err := router.Submit(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := r.Wait(ctx)
		if err != nil || !slices.Equal(targets, []string{"record"}) {
			t.Fatalf("unsafe release feedback dispatched instead of recorded: meta=%v targets=%v err=%v", m.Meta, targets, err)
		}
	}
	checkRecorded("echoed-review")
	send("new-work-suggestion", "task_result", "SUGGEST_NEW_WORK SOURCE_DIGEST="+digest, []string{buildTask}, false)
	checkRecorded("new-work-suggestion")
	long := make([]string, 16)
	for i := range long {
		long[i] = fmt.Sprintf("upstream-review-%d", i)
	}
	send("overlong-chain", "task_result", "SOURCE_DIGEST="+digest, long, true)
	checkRecorded("overlong-chain")
	for _, spec := range []struct {
		id   string
		meta map[string]any
	}{
		{"execution-echo", map[string]any{"target_session_id": initial.Meta["execution_id"], "feedback_chain": []string{task}}},
		{"source-task-echo", map[string]any{"task_id": task, "source_task_id": task}},
	} {
		spec.meta["kind"], spec.meta["feedback_id"] = "task_result", spec.id
		if _, err := builder.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": "SOURCE_DIGEST=" + digest}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: spec.meta}); err != nil {
			t.Fatal(err)
		}
		checkRecorded(spec.id)
	}
	// A genuine operator update is not feedback, even with a copied chain/ID.
	send("echoed-review", "ci_notice", "SOURCE_DIGEST="+digest, []string{task}, true)
	ordinary := waitFor(t, ctx, wires)
	if got := activeSubmit(t, ctx, router, ordinary); got != task {
		t.Fatalf("ordinary update misrouted: %s", got)
	}
	waitFor(t, ctx, feedback)
	db, err := sql.Open("sqlite", filepath.Join(state, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for {
		var active int
		if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM messages WHERE state!='done') + (SELECT count(*) FROM inputs WHERE state!='done') + (SELECT count(*) FROM outbox WHERE state!='sent')`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active == 0 {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	var tasks, attempts, inputs int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM tasks),(SELECT count(*) FROM attempts),(SELECT count(*) FROM inputs)").Scan(&tasks, &attempts, &inputs); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "review-audit.txt"))
	if err != nil || string(b) != strings.Repeat(digest+"\n", 2) || tasks != 1 || attempts != 3 || inputs != 3 {
		t.Fatalf("release safety boundary failed: tasks=%d attempts=%d inputs=%d audit=%q err=%v", tasks, attempts, inputs, b, err)
	}
	summary, err := json.MarshalIndent(map[string]any{"source_digest": digest, "builder_task": buildTask, "reviewer_task": task, "tasks": tasks, "attempts": attempts, "inputs": inputs, "audit_rows": 2, "unsafe_feedback_recorded": 5}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case-summary.json"), append(summary, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("release source digest audited; offline cyclic feedback, implicit new work and hop overflow recorded; independent update processed; tasks=%d attempts=%d evidence=%s", tasks, attempts, dir)
}
