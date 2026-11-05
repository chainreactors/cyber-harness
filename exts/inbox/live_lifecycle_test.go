package inbox_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	harness "github.com/chainreactors/cyber/core/collaboration"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	nativeext "github.com/chainreactors/cyber/exts/native"
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

// Every successful routing and execution decision is made by the real model.
// Inject failure after an actual side effect, with an accepted correction still
// unread; recovery must retain that correction without repeating the side effect.
func TestLiveActiveModelFailureRetainsCorrection(t *testing.T) {
	model := liveRoutingModel(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	if parent := os.Getenv("CYBER_INBOX_CASES_OUTPUT"); parent != "" {
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(parent, "live-provider-fault-")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan string, 1)
	fail := make(chan struct{})
	results := make(chan mailbox.Message, 16)
	var injected atomic.Bool
	var calls atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		isRouter := len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route"
		if !isRouter {
			for _, message := range r.Messages {
				for _, part := range message.Content {
					if result := part.GetToolResult(); result != nil && strings.Contains(coretool.ResultText(result), "STAGE_ONE_DONE") && injected.CompareAndSwap(false, true) {
						blocked <- r.SessionID
						select {
						case <-fail:
							return nil, errors.New("injected connection failure after committed stage one")
						case <-callCtx.Done():
							return nil, callCtx.Err()
						}
					}
				}
			}
		}
		calls.Add(1)
		return model.ChatCompletion(callCtx, r)
	})
	config := inboxext.Config{StateDir: filepath.Join(dir, "state"), MaxActive: 2, Instructions: "Create one anonymous new_task per initial request, label fault-worker or audit-worker according to its ROLE. Honor explicit task_id for corrections. For owned subagent_feedback choose action=record (the host must deliver it). Never create work from feedback.", Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}
	_, router, _, set := activeHost(t, p, config)
	defer func() { closeActive(t, set) }()
	initial := mailbox.NewUserMessage(fmt.Sprintf("ROLE=fault-worker. Work only in %q using bash; no subagents or external services. First execute exactly one bash command that writes stage-one.txt containing the exact ASCII text ALPHA-73129 (no newline), appends exactly one line 'stage-one' to executions.log, and prints STAGE_ONE_DONE. Never repeat this completed step, even on recovery. Then await the next scope instruction before producing final work. Do not create stage-two.json yet.", filepath.ToSlash(dir)))
	initial.Message.Id = "fault-request"
	task := activeSubmit(t, ctx, router, initial)
	first := waitFor(t, ctx, blocked)
	audit := activeSubmit(t, ctx, router, mailbox.NewUserMessage(fmt.Sprintf("ROLE=audit-worker. Work only in %q. Use bash to read the actual stage-one.txt and write audit.txt containing its exact bytes. Do not change other files or create subagents. Finish with AUDIT_READY.", filepath.ToSlash(dir))))
	for {
		m := waitFor(t, ctx, results)
		if m.Meta.TaskID == audit {
			if m.Meta.Status != "completed" {
				t.Fatalf("audit failed: %v", m.Meta)
			}
			break
		}
	}
	correction := mailbox.NewMessage(mailbox.OriginPeer, "user", fmt.Sprintf("Scope correction: finish the existing task using stage-one.txt in %q. Do not repeat stage one or append executions.log. Write stage-two.json with keys token (exact stage-one contents), checksum (lowercase SHA-256 of the actual file bytes), and revision (integer 2). Re-read it to verify, then finish with CORRECTION_APPLIED. Do not modify audit.txt, inputs, or other files.", filepath.ToSlash(dir)))
	correction.Message.Id = "scope-correction"
	correction.Meta = mailbox.Metadata{TaskID: task, Sender: "monitor", SourceSessionID: "fault-scope"}
	activeSubmit(t, ctx, router, correction)
	close(fail)
	failed, completed := false, false
	var last mailbox.Message
	for !failed || !completed {
		m := waitFor(t, ctx, results)
		if m.Meta.TaskID != task || m.Meta.ReplyMessage != initial.Message.Id {
			t.Fatalf("lost task or requester: %v", m.Meta)
		}
		switch m.Meta.Status {
		case "failed":
			failed = true
		case "completed":
			completed = true
			last = m
		default:
			t.Fatalf("unexpected result %v", m.Meta)
		}
	}
	if last.Meta.ExecutionID == first || !strings.Contains(provider.MessageText(last.Message), "CORRECTION_APPLIED") {
		t.Fatalf("unread correction was not recovered: %v %s", last.Meta, provider.MessageText(last.Message))
	}
	b, err := os.ReadFile(filepath.Join(dir, "stage-two.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Token    string
		Checksum string
		Revision int
	}
	if err := json.Unmarshal(b, &artifact); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("ALPHA-73129"))
	if artifact.Token != "ALPHA-73129" || artifact.Checksum != hex.EncodeToString(sum[:]) || artifact.Revision != 2 {
		t.Fatalf("incorrect artifact: %s", b)
	}
	log, err := os.ReadFile(filepath.Join(dir, "executions.log"))
	if err != nil || strings.TrimSpace(string(log)) != "stage-one" {
		t.Fatalf("completed side effect repeated: %q %v", log, err)
	}
	proof, err := os.ReadFile(filepath.Join(dir, "audit.txt"))
	if err != nil || string(proof) != artifact.Token {
		t.Fatalf("independent audit failed: %q %v", proof, err)
	}
	closeActive(t, set)
	_, router, _, set = activeHost(t, p, config)
	before := calls.Load()
	if got := activeSubmit(t, ctx, router, correction); got != task {
		t.Fatalf("replay changed task: %s", got)
	}
	time.Sleep(300 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("duplicate correction called model after restart")
	}
	t.Logf("real model recovered unread correction after committed command failure; side effect once, independent audit, forced record delivered, duplicate after restart model calls=0; artifacts=%s", dir)
}

func TestLiveWaitingCapacityAndTmuxWake(t *testing.T) {
	for _, capacity := range []int{1, 4} {
		t.Run(fmt.Sprintf("capacity=%d", capacity), func(t *testing.T) {
			model, dir := liveRoutingModel(t), t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			waits := make(chan string, 16)
			results := make(chan mailbox.Message, 16)
			f := apptest.NewFixture(t, nil, nil)
			f.Providers.Set(model, provider.ProviderConfig{Model: os.Getenv("CYBER_MODEL"), ContextWindow: 200000, MaxTokens: 8192})
			observer := f.Stream.Observe(func(ev *aop.Event) {
				if call := ev.GetToolCall(); call != nil && call.Name == "inbox_wait" {
					select {
					case waits <- ev.SessionId:
					default:
					}
				}
			})
			defer observer.Close(context.Background())
			router := inboxext.New(inboxext.Config{StateDir: filepath.Join(dir, "state"), MaxActive: capacity, Instructions: "Create one independent new_task per initial user request, omit name. Honor explicit task_id for followups. For subagent_feedback use action=reply. Never create work from feedback.", Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }})
			entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), nativeext.New(), sessionext.New(session.Config{}), subagentext.NewTools(), router)
			set := hosttest.Load(t, ctx, entries...)
			defer closeActive(t, set)
			executions := make(map[string]string)
			for i := 0; i < capacity; i++ {
				task := activeSubmit(t, ctx, router.Router(), mailbox.NewUserMessage(fmt.Sprintf("ROLE=coordinator-%d. This is a mailbox lifecycle test. Your first action MUST be inbox_wait with timeout=0 (omit timeout or use 0). Wait for a later message containing RESUME_TOKEN. Do not use bash, poll, inspect files, create subagents, or finish before that message. Once it arrives, answer exactly RESUME_TOKEN and finish.", i)))
				got := waitFor(t, ctx, waits)
				executions[task] = got
				t.Logf("real model entered inbox_wait: session=%s", got)
			}
			producer := activeSubmit(t, ctx, router.Router(), mailbox.NewUserMessage("ROLE=producer. Independently answer exactly PRODUCER_READY and finish. Do not use tools or create subagents."))
			proof := waitFor(t, ctx, results)
			if proof.Meta.TaskID != producer || proof.Meta.Status != "completed" || strings.TrimSpace(provider.MessageText(proof.Message)) != "PRODUCER_READY" {
				t.Fatalf("producer did not finish while ALL coordinators remained suspended: %v %s", proof.Meta, provider.MessageText(proof.Message))
			}
			t.Logf("producer completed before any waiter was woken: task=%s capacity=%d", producer, capacity)
			// Resume through the actual tmux input path without replacing executions.
			for _, info := range f.Shell.Manager().List() {
				if info.Kind == "agent" && info.Name != "router" && info.State.Active() {
					args, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("tmux send-keys -t %s RESUME_TOKEN Enter", info.ID)})
					result, err := f.Shell.Execute(ctx, string(args))
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("tmux wake: %s", coretool.ResultText(result))
				}
			}
			seen := map[string]bool{producer: true}
			for len(seen) < capacity+1 {
				m := waitFor(t, ctx, results)
				if m.Meta.Status != "completed" {
					t.Fatalf("worker failed: %v %s", m.Meta, provider.MessageText(m.Message))
				}
				id := m.Meta.TaskID
				seen[id] = true
				text := provider.MessageText(m.Message)
				if strings.TrimSpace(text) != "RESUME_TOKEN" || executions[id] != m.Meta.ExecutionID {
					t.Errorf("waiter lost output or original execution: task=%s meta=%v output=%q", id, m.Meta, text)
				}
				t.Logf("completed after tmux wake: task=%s execution=%v output=%q", id, m.Meta.ExecutionID, text)
			}
		})
	}
}

func TestLiveActiveIOAClosedLoop(t *testing.T) {
	model, dir := liveRoutingModel(t), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "live-review")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://live-review@", 1)
	peer, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.EnsureRegistered(ctx, "live-review-requester", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := peer.Space(ctx, "live-review", "real-model inbox integration")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan string, 4)
	var calls atomic.Int32
	p := modelFunc(func(c context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		calls.Add(1)
		return model.ChatCompletion(c, r)
	})
	var receiver string
	open := func() (*inboxext.Router, *extension.Set, *ioaclient.Extension) {
		f := apptest.NewFixture(t, nil, nil)
		f.Providers.Set(p, provider.ProviderConfig{Model: os.Getenv("CYBER_MODEL"), ContextWindow: 200000, MaxTokens: 8192})
		observer := f.Stream.Observe(func(ev *aop.Event) {
			if call := ev.GetToolCall(); call != nil && call.Name == "inbox_wait" {
				select {
				case waiting <- ev.SessionId:
				default:
				}
			}
		})
		t.Cleanup(func() { _ = observer.Close(context.Background()) })
		opts, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: filepath.Join(dir, "state")}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": "live-review"}}})
		if err != nil {
			t.Fatal(err)
		}
		connection := ioaclient.New(*opts)
		router := inboxext.New(inboxext.Config{StateDir: filepath.Join(dir, "state"), MaxActive: 1, Instructions: "Create an anonymous new_task with label ioa-review-worker for an initial request. Honor explicit task_id on all followups. Reply to subagent_feedback using action=reply. Never create tasks from feedback.", Notify: func(c context.Context, m mailbox.Message) error {
			node := m.Meta.ReplyNode
			ref := m.Meta.ReplyMessage
			_, err := connection.Service().Client().Send(c, space.ID, protocols.SendMessage{Content: map[string]any{"text": provider.MessageText(m.Message)}, Refs: &protocols.Ref{Nodes: []string{node}, Messages: []string{ref}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.TaskResult, ID: m.Message.Id, Source: harness.Address{TaskID: m.Meta.TaskID, SessionID: m.Meta.ExecutionID}, Chain: m.Meta.FeedbackChain}.Meta()})
			return err
		}})
		adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: router.Router().Deliver, LoadCursor: router.Router().LoadCursor, SaveCursor: router.Router().SaveCursor})
		entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), nativeext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, router)
		set := hosttest.Load(t, ctx, entries...)
		if _, err := connection.Service().ReadySpace(ctx); err != nil {
			t.Fatal(err)
		}
		id := connection.Service().Client().NodeID()
		if receiver != "" && receiver != id {
			t.Fatal("IOA identity changed")
		}
		receiver = id
		return router.Router(), set, connection
	}
	router, set, connection := open()
	defer func() { closeActive(t, set) }()
	send := func(text, task string) protocols.Message {
		record := harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "external-live-review"}, Target: harness.Address{TaskID: task}}
		m, err := peer.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": text}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: record.Meta()})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	initial := send("This is a mailbox integration test. First call inbox_wait with timeout=0 and wait for TOKEN=73129. Do not run shell commands, poll or finish before the token arrives. Once the token arrives, answer exactly VERIFIED 73129 and finish.", "")
	execution := waitFor(t, ctx, waiting)
	db, err := sql.Open("sqlite", filepath.Join(dir, "state", "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var task string
	if err := db.QueryRow("SELECT task_id FROM attempts WHERE session_id=?", execution).Scan(&task); err != nil {
		t.Fatal(err)
	}
	send("TOKEN=73129. You have now received the evidence. Finish with exactly VERIFIED 73129.", task)
	waitReply := func(after, text string) protocols.Message {
		for {
			messages, err := peer.Read(ctx, space.ID, protocols.ReadOptions{All: true, After: after})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range messages {
				record, parseErr := harness.Parse(m.Meta)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				if record != nil && m.Sender == receiver && record.Event == harness.TaskResult && record.Source.TaskID == task && strings.Contains(fmt.Sprint(m.Content), text) {
					return m
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	reply := waitReply(initial.ID, "VERIFIED 73129")
	record, parseErr := harness.Parse(reply.Meta)
	if parseErr != nil || record == nil || record.Source.TaskID != task || record.Source.SessionID != execution {
		t.Fatalf("wrong IOA feedback identity %v", reply.Meta)
	}
	t.Logf("IOA initial->router->wait->followup->same execution->feedback->peer: task=%s execution=%s", task, execution)
	closeActive(t, set)
	offline := send("Continue your previous task after host restart. Recall the token from your history; answer exactly RESTORED 73129. Do not use tools.", task)
	router, set, connection = open()
	_ = router
	_ = connection
	reply = waitReply(offline.ID, "RESTORED 73129")
	record, parseErr = harness.Parse(reply.Meta)
	if parseErr != nil || record == nil || record.Source.TaskID != task || record.Source.SessionID == execution {
		t.Fatalf("restart did not preserve task/change execution %v", reply.Meta)
	}
	// Replay the exact source envelope; success must not call any model.
	before := calls.Load()
	duplicate := mailbox.NewMessage(mailbox.OriginPeer, "user", "duplicate source event")
	duplicate.Message.Id = offline.ID
	duplicate.Meta = mailbox.Metadata{MessageID: offline.ID, Sender: peer.NodeID(), SourceSessionID: "external-live-review", TaskID: task}
	if got := activeSubmit(t, ctx, router, duplicate); got != task {
		t.Fatalf("duplicate task=%s", got)
	}
	time.Sleep(400 * time.Millisecond)
	if calls.Load() != before {
		t.Error("duplicate replay called the model")
	}
	t.Logf("offline IOA replay restored history; same task new execution; duplicate replay model calls=0; total real calls=%d", calls.Load())
}
