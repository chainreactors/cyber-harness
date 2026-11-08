package inbox_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	nativeext "github.com/chainreactors/cyber/exts/native"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	"github.com/chainreactors/utils/proc"
)

func TestActiveProducerWaitYieldsCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	registered := make(chan struct{}, 1)
	waiting := make(chan string, 1)
	results := make(chan mailbox.Message, 4)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if lastInput(r) == "automatic producer wait" {
			return response("awaiting background evidence"), nil
		}
		return response("evidence processed"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), MaxActive: 1, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }})
	defer closeActive(t, set)
	var handle *mailbox.ProducerHandle
	agenthooks.BeforeModel.On(f.Hooks, "test.producer", func(callCtx context.Context, e agenthooks.ContextEvent) ([]*aop.Message, error) {
		for _, m := range e.Messages {
			if provider.MessageText(m) == "automatic producer wait" && handle == nil {
				cfg, _ := agent.ToolAgentConfig(callCtx)
				handle = cfg.Inbox.RegisterProducer("external-evidence")
				registered <- struct{}{}
			}
		}
		return nil, nil
	})
	agenthooks.InboxWait.On(f.Hooks, "test.suspend", func(_ context.Context, e agenthooks.InboxWaitEvent) (struct{}, error) {
		if e.Waiting {
			waiting <- e.SessionID
		}
		return struct{}{}, nil
	})
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("automatic producer wait"))
	waitFor(t, ctx, registered)
	execution := waitFor(t, ctx, waiting)
	producer := activeSubmit(t, ctx, router, mailbox.NewUserMessage("independent producer"))
	if m := waitFor(t, ctx, results); m.Meta.TaskID != producer {
		t.Fatalf("waiting task blocked producer: %v", m.Meta)
	}
	message := mailbox.NewUserMessage("background evidence ready")
	message.Meta = mailbox.Metadata{TaskID: task}
	activeSubmit(t, ctx, router, message)
	handle.Done()
	m := waitFor(t, ctx, results)
	if m.Meta.TaskID != task || m.Meta.ExecutionID != execution {
		t.Fatalf("automatic wait did not resume original execution: %v", m.Meta)
	}
}

// All waiters wake while a producer owns the only slot. Cancel one contender,
// then require every other waiter to resume in its ORIGINAL execution, serially.
func TestActiveWaitWakeContentionAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	waiting := make(chan string, 8)
	resumed := make(chan string, 8)
	producer := make(chan struct{}, 1)
	release := make(chan struct{})
	results := make(chan mailbox.Message, 8)
	var executing, peak atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		n := executing.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer executing.Add(-1)
		if strings.HasPrefix(lastInput(r), "waiter-") {
			return toolResponse("inbox_wait", map[string]any{}), nil
		}
		if lastInput(r) == "producer holds slot" {
			producer <- struct{}{}
			select {
			case <-release:
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		} else {
			resumed <- r.SessionID
			select {
			case <-time.After(30 * time.Millisecond):
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		return response("done"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), MaxActive: 1, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}, nativeext.New())
	defer closeActive(t, set)
	sub := agenthooks.InboxWait.On(f.Hooks, "test.suspended", func(_ context.Context, e agenthooks.InboxWaitEvent) (struct{}, error) {
		if e.Waiting {
			waiting <- e.SessionID
		}
		return struct{}{}, nil
	})
	defer sub.Close(context.Background())
	tasks := make([]string, 4)
	sessions := make(map[string]bool)
	for i := range tasks {
		tasks[i] = activeSubmit(t, ctx, router, mailbox.NewUserMessage(fmt.Sprintf("waiter-%d", i)))
		sessions[waitFor(t, ctx, waiting)] = true
	}
	activeSubmit(t, ctx, router, mailbox.NewUserMessage("producer holds slot"))
	waitFor(t, ctx, producer)
	for _, task := range tasks {
		m := mailbox.NewUserMessage("resume")
		m.Meta = mailbox.Metadata{TaskID: task}
		activeSubmit(t, ctx, router, m)
	}
	select {
	case id := <-resumed:
		t.Fatalf("waiter resumed while producer owned capacity: %s", id)
	case <-time.After(100 * time.Millisecond):
	}
	for _, info := range f.Shell.Manager().List() {
		if info.Name == tasks[0] && info.State.Active() {
			if err := f.Shell.Manager().Stop(info.ID, proc.StopOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := waitFor(t, ctx, results)
	if m.Meta.TaskID != tasks[0] || m.Meta.Status != "canceled" {
		t.Fatalf("cancellation while reacquiring failed: %v", m.Meta)
	}
	close(release)
	for range 4 {
		m := waitFor(t, ctx, results)
		if m.Meta.Status != "completed" {
			t.Fatalf("failed resumed task: %v", m.Meta)
		}
	}
	for range 3 {
		id := waitFor(t, ctx, resumed)
		if !sessions[id] {
			t.Fatalf("resume created another execution: %s", id)
		}
		delete(sessions, id)
	}
	if peak.Load() != 1 {
		t.Fatalf("MaxActive exceeded under simultaneous wakeup: peak=%d", peak.Load())
	}
}

func TestActiveRestartWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	waiting := make(chan string, 4)
	results := make(chan mailbox.Message, 4)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		for _, m := range r.Messages {
			if strings.Contains(provider.MessageText(m), "RESUME_AFTER_RESTART") {
				return response("restored"), nil
			}
		}
		return toolResponse("inbox_wait", map[string]any{}), nil
	})
	config := inboxext.Config{StateDir: t.TempDir(), MaxActive: 1, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}
	f, router, _, set := activeHost(t, p, config, nativeext.New())
	sub := agenthooks.InboxWait.On(f.Hooks, "test.suspended", func(_ context.Context, e agenthooks.InboxWaitEvent) (struct{}, error) {
		if e.Waiting {
			waiting <- e.SessionID
		}
		return struct{}{}, nil
	})
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("original waiting contract"))
	old := waitFor(t, ctx, waiting)
	closeActive(t, set)
	_ = sub.Close(context.Background())
	_, router, _, set = activeHost(t, p, config, nativeext.New())
	defer closeActive(t, set)
	m := mailbox.NewUserMessage("RESUME_AFTER_RESTART")
	m.Meta = mailbox.Metadata{TaskID: task}
	activeSubmit(t, ctx, router, m)
	done := waitFor(t, ctx, results)
	if done.Meta.TaskID != task || done.Meta.ExecutionID == old || done.Meta.Status != "completed" {
		t.Fatalf("waiting restart lost identity or did not complete: %v", done.Meta)
	}
}

func TestActiveWaitingWorkerReleasesCapacity(t *testing.T) {
	for _, capacity := range []int{1, 4} {
		t.Run(fmt.Sprintf("capacity=%d", capacity), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			waiting := make(chan struct{}, capacity)
			producerStarted := make(chan struct{}, 1)
			p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
				if result, router := activeDecision(r); router {
					return result, nil
				}
				if strings.Contains(lastInput(r), "coordinator waits") {
					waiting <- struct{}{}
					return toolResponse("inbox_wait", map[string]any{}), nil
				}
				producerStarted <- struct{}{}
				return response("producer evidence"), nil
			})
			dir := t.TempDir()
			f := apptest.NewFixture(t, nil, nil)
			f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
			router := inboxext.New(inboxext.Config{StateDir: dir, MaxActive: capacity})
			entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), nativeext.New(), sessionext.New(session.Config{}), subagentext.NewTools(), router)
			set := hosttest.Load(t, t.Context(), entries...)
			defer closeActive(t, set)
			for i := 0; i < capacity; i++ {
				activeSubmit(t, ctx, router.Router(), mailbox.NewUserMessage(fmt.Sprintf("coordinator waits for producer evidence %d", i)))
				waitFor(t, ctx, waiting)
			}
			producer := activeSubmit(t, ctx, router.Router(), mailbox.NewUserMessage("producer generates evidence for coordinator"))
			select {
			case <-producerStarted:
			case <-time.After(1500 * time.Millisecond):
				db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var running int
				var second string
				if err := db.QueryRow("SELECT count(*) FROM tasks WHERE state='running'").Scan(&running); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT state FROM tasks WHERE id=?", producer).Scan(&second); err != nil {
					t.Fatal(err)
				}
				t.Fatalf("waiting workers never release capacity: running=%d producer=%s maxActive=%d", running, second, capacity)
			}
		})
	}
}

func TestActiveNamedPreparationReachesModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	observed := make(chan string, 1)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("admitted"), nil
			}
			if strings.Contains(lastInput(r), "subagent_feedback") {
				return toolResponse("inbox_route", map[string]any{"action": "record"}), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"name": "prepared"}}), nil
		}
		observed <- lastInput(r)
		return response("finished"), nil
	})
	f := apptest.NewFixture(t, nil, nil)
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	definition := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope, subagent.Subagent{Name: "prepared", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
			return cfg, "PREPARED CONTRACT: " + input.Prompt, nil
		}})
	}}
	router := inboxext.New(inboxext.Config{StateDir: t.TempDir()})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), definition, sessionext.New(session.Config{}), subagentext.NewTools(), router)
	set := hosttest.Load(t, t.Context(), entries...)
	defer closeActive(t, set)
	activeSubmit(t, ctx, router.Router(), mailbox.NewUserMessage("raw request"))
	got := waitFor(t, ctx, observed)
	if !strings.Contains(got, "PREPARED CONTRACT") {
		t.Fatalf("named subagent prepared task was discarded; actual model input=%q", got)
	}
}

func TestActiveAddressByNamePersistsWorkerInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("admitted"), nil
			}
			if strings.Contains(lastInput(r), "important correction while worker is busy") {
				return toolResponse("inbox_route", map[string]any{"session_id": "stable-worker"}), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"label": "stable-worker"}}), nil
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-callCtx.Done()
		return nil, callCtx.Err()
	})
	dir := t.TempDir()
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("initial worker task"))
	waitFor(t, ctx, started)
	correction := mailbox.NewMessage(mailbox.OriginPeer, "user", "important correction while worker is busy")
	correction.Meta = mailbox.Metadata{Sender: "remote-node", SourceSessionID: "remote-execution", TargetSessionID: "stable-worker"}
	receipt, err := router.Submit(ctx, correction)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := receipt.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM inputs WHERE task_id=?", task).Scan(&count); err != nil {
		t.Fatal(err)
	}
	t.Logf("name-addressed follow-up: targets=%v persisted inputs=%d want=2", targets, count)
	closeActive(t, set)
	requests := make(chan *provider.ChatCompletionRequest, 1)
	restored := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		requests <- r
		return response("recovered"), nil
	})
	_, _, _, reopened := activeHost(t, restored, inboxext.Config{StateDir: dir})
	defer closeActive(t, reopened)
	request := waitFor(t, ctx, requests)
	for _, m := range request.Messages {
		if strings.Contains(provider.MessageText(m), "important correction while worker is busy") {
			return
		}
	}
	t.Fatalf("acknowledged name-addressed correction disappeared after host restart; persisted inputs=%d", count)
}
