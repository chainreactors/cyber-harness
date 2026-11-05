package inbox_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	harness "github.com/chainreactors/cyber/core/collaboration"
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

// A completed release check is delivered over real HTTP/SSE. The recipient
// records the actual repository revision, then the sender loses its delivery
// acknowledgement and retries its durable outbox. Both transport messages must
// refer to one logical result and cause one repository audit entry.
func TestRepositoryCIResultDeliveryRetryIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("repository command and IOA integration")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
		dir, err = os.MkdirTemp(parent, "result-delivery-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("result delivery evidence: %s", dir)
	quote := func(path string) string {
		return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\"'\"'") + "'"
	}
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "ci-delivery")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://ci-delivery@", 1)
	sender, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.EnsureRegistered(ctx, "ci-result-producer", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := sender.Space(ctx, "release-results", "repository CI results")
	if err != nil {
		t.Fatal(err)
	}
	revisionPattern := regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	var auditCommands, buildCommands, sends atomic.Int32
	collectorFeedback := make(chan mailbox.Message, 8)
	wires := make(chan mailbox.Message, 8)
	feedbackIDs := make(chan string, 4)
	collectorModel := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if !strings.Contains(lastInput(r), "release-ci-result:") {
			return response("waiting for release evidence"), nil
		}
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, c := range r.Messages[len(r.Messages)-1].Content {
				if result := c.GetToolResult(); result != nil && result.IsError {
					return nil, fmt.Errorf("audit command failed: %s", coretool.ResultText(result))
				}
			}
			return response("release evidence recorded"), nil
		}
		revision := revisionPattern.FindString(lastInput(r))
		if revision == "" {
			return nil, errors.New("release result contained no repository revision")
		}
		auditCommands.Add(1)
		return toolResponse("bash", map[string]any{"command": fmt.Sprintf("printf '%%s\\n' %s >> %s", quote(revision), quote(filepath.Join(dir, "release-audit.txt"))), "timeout": 10}), nil
	})
	f := apptest.NewFixture(t, nil, nil)
	f.Providers.Set(collectorModel, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	receiverState := filepath.Join(dir, "collector-state")
	connectionConfig, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: receiverState}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": "release-results"}}})
	if err != nil {
		t.Fatal(err)
	}
	connection := ioaclient.New(*connectionConfig)
	routerExt := inboxext.New(inboxext.Config{StateDir: receiverState, Notify: func(_ context.Context, m mailbox.Message) error { collectorFeedback <- m; return nil }})
	router := routerExt.Router()
	adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: func(callCtx context.Context, m mailbox.Message) error {
		err := router.Deliver(callCtx, m)
		if err == nil && strings.Contains(provider.MessageText(m.Message), "release-ci-result:") {
			wires <- m
		}
		return err
	}, LoadCursor: router.LoadCursor, SaveCursor: router.SaveCursor})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, routerExt)
	receiverSet := hosttest.Load(t, ctx, entries...)
	defer func() { closeActive(t, receiverSet) }()
	if _, err := connection.Service().ReadySpace(ctx); err != nil {
		t.Fatal(err)
	}
	collector := activeSubmit(t, ctx, router, mailbox.NewUserMessage("Collect the next finished build evidence in the release audit."))
	if m := waitFor(t, ctx, collectorFeedback); m.Meta.Status != "completed" {
		t.Fatalf("collector did not start: %v", m.Meta)
	}
	producerModel := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, c := range r.Messages[len(r.Messages)-1].Content {
				if result := c.GetToolResult(); result != nil {
					if result.IsError {
						return nil, fmt.Errorf("repository revision command failed: %s", coretool.ResultText(result))
					}
					if revision := revisionPattern.FindString(coretool.ResultText(result)); revision != "" {
						return response("release-ci-result:" + revision), nil
					}
				}
			}
			return nil, errors.New("git did not return a repository revision")
		}
		buildCommands.Add(1)
		return toolResponse("bash", map[string]any{"command": fmt.Sprintf("cd %s && git rev-parse HEAD > %s && cat %s", quote(root), quote(filepath.Join(dir, "producer-revision.txt")), quote(filepath.Join(dir, "producer-revision.txt"))), "timeout": 10}), nil
	})
	_, producer, _, producerSet := activeHost(t, producerModel, inboxext.Config{StateDir: filepath.Join(dir, "producer-state"), Notify: func(callCtx context.Context, m mailbox.Message) error {
		_, err := sender.Send(callCtx, space.ID, protocols.SendMessage{Content: map[string]any{"text": provider.MessageText(m.Message)}, Refs: &protocols.Ref{Nodes: []string{connection.Service().Client().NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.TaskResult, ID: m.Message.Id, Target: harness.Address{TaskID: collector}}.Meta()})
		if err != nil {
			return err
		}
		feedbackIDs <- m.Message.Id
		if sends.Add(1) == 1 {
			select {
			case recorded := <-collectorFeedback:
				if recorded.Meta.TaskID != collector || recorded.Meta.Status != "completed" {
					return fmt.Errorf("collector failed to record result: %v", recorded.Meta)
				}
			case <-callCtx.Done():
				return callCtx.Err()
			}
			return errors.New("injected acknowledgement loss after recipient committed")
		}
		return nil
	}})
	defer closeActive(t, producerSet)
	activeSubmit(t, ctx, producer, mailbox.NewUserMessage("Produce the actual repository revision as release CI evidence."))
	first, second := waitFor(t, ctx, wires), waitFor(t, ctx, wires)
	if first.Message.Id == second.Message.Id {
		t.Fatal("retry did not produce distinct IOA transport message IDs")
	}
	feedbackID, retryID := waitFor(t, ctx, feedbackIDs), waitFor(t, ctx, feedbackIDs)
	if feedbackID != retryID || first.Meta.FeedbackID != feedbackID || second.Meta.FeedbackID != feedbackID || first.Meta.MessageID != first.Message.Id || second.Meta.MessageID != second.Message.Id {
		t.Fatalf("transport/result identity was lost: feedback=%s retry=%s first=%v second=%v", feedbackID, retryID, first.Meta, second.Meta)
	}
	receipt, err := router.Submit(ctx, second)
	if err != nil || receipt.ID != first.Message.Id {
		t.Fatalf("retry replaced the original reply reference: receipt=%v err=%v", receipt, err)
	}
	if targets, err := receipt.Wait(ctx); err != nil || len(targets) != 1 || targets[0] != "task:"+collector {
		t.Fatalf("retry changed the logical destination: %v %v", targets, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(receiverState, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	waitSettled := func() {
		for {
			var active int
			if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM messages WHERE state IN ('pending','routing')) + (SELECT count(*) FROM inputs WHERE state IN ('pending','running')) + (SELECT count(*) FROM tasks WHERE state IN ('queued','running')) + (SELECT count(*) FROM outbox WHERE state='pending')`).Scan(&active); err != nil {
				t.Fatal(err)
			}
			if active == 0 {
				return
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal("receiver did not settle after delivery retries")
			}
		}
	}
	waitSettled()
	var inputs int
	if err := db.QueryRow("SELECT count(*) FROM inputs WHERE task_id=?", collector).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile(filepath.Join(dir, "release-audit.txt"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if inputs != 2 || auditCommands.Load() != 1 || buildCommands.Load() != 1 || sends.Load() != 2 || strings.TrimSpace(string(audit)) != strings.TrimSpace(string(revision)) {
		t.Fatalf("same CI feedback executed twice after acknowledgement loss: inputs=%d audit_commands=%d build_commands=%d sends=%d audit=%q", inputs, auditCommands.Load(), buildCommands.Load(), sends.Load(), audit)
	}
	retryAudits := auditCommands.Load()
	// Equal result IDs from another sender, and ordinary notifications which
	// happen to carry feedback_id, are independent events rather than retries.
	other, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.EnsureRegistered(ctx, "independent-ci-producer", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		client  *peerclient.Client
		kind    string
		session string
	}{{other, "task_result", ""}, {sender, "ci_notice", ""}, {sender, "task_result", "independent-source-session"}} {
		event := harness.Message
		if spec.kind == "task_result" {
			event = harness.TaskResult
		}
		if _, err := spec.client.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": "release-ci-result:" + strings.TrimSpace(string(revision))}, Refs: &protocols.Ref{Nodes: []string{connection.Service().Client().NodeID()}}, Meta: harness.Envelope{Version: harness.Version, Event: event, ID: feedbackID, Target: harness.Address{TaskID: collector}, Source: harness.Address{SessionID: spec.session}}.Meta()}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, ctx, wires)
		if result := waitFor(t, ctx, collectorFeedback); result.Meta.TaskID != collector || result.Meta.Status != "completed" {
			t.Fatalf("independent result was suppressed: %v", result.Meta)
		}
	}
	if err := db.QueryRow("SELECT count(*) FROM inputs WHERE task_id=?", collector).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if inputs != 5 || auditCommands.Load() != 4 {
		t.Fatalf("result deduplication crossed sender/type boundaries: inputs=%d audits=%d", inputs, auditCommands.Load())
	}
	audit, err = os.ReadFile(filepath.Join(dir, "release-audit.txt"))
	if err != nil || string(audit) != strings.Repeat(strings.TrimSpace(string(revision))+"\n", 4) {
		t.Fatalf("independent results did not each produce one real audit entry: audit=%q err=%v", audit, err)
	}
	closeActive(t, receiverSet)
	_, router, _, receiverSet = activeHost(t, collectorModel, inboxext.Config{StateDir: receiverState, Notify: func(_ context.Context, m mailbox.Message) error { collectorFeedback <- m; return nil }})
	receipt, err = router.Submit(ctx, second)
	if err != nil || receipt.ID != first.Message.Id {
		t.Fatalf("persisted retry identity was not restored: receipt=%v err=%v", receipt, err)
	}
	if _, err := receipt.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	waitSettled()
	var attempts int
	if err := db.QueryRow("SELECT count(*) FROM attempts WHERE task_id=?", collector).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 5 || auditCommands.Load() != 4 {
		t.Fatalf("restart replayed an acknowledged result: attempts=%d audits=%d", attempts, auditCommands.Load())
	}
	audit, err = os.ReadFile(filepath.Join(dir, "release-audit.txt"))
	if err != nil || string(audit) != strings.Repeat(strings.TrimSpace(string(revision))+"\n", 4) {
		t.Fatalf("restart changed the committed audit: audit=%q err=%v", audit, err)
	}
	trace, err := json.MarshalIndent(map[string]any{"feedback_id": feedbackID, "transport_ids": []string{first.Message.Id, second.Message.Id}, "outbox_sends": sends.Load(), "retry_audits": retryAudits, "total_audits": auditCommands.Load(), "producer_commands": buildCommands.Load(), "collector_inputs": inputs, "collector_attempts_after_restart": attempts, "revision": strings.TrimSpace(string(revision))}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case-summary.json"), append(trace, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("retry pair produced one audit; three independent sender/type/session events produced three additional audits; sends=%d audits=%d", sends.Load(), auditCommands.Load())
}
