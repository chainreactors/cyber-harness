package inbox_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	coretool "github.com/chainreactors/cyber/core/tool"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
)

func TestActiveUnknownNameRejectedBeforeAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	feedback := make(chan mailbox.Message, 4)
	var corrected atomic.Bool
	var workers atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				for _, part := range r.Messages[len(r.Messages)-1].Content {
					if result := part.GetToolResult(); result != nil && result.IsError && strings.Contains(coretool.ResultText(result), "omit new_task.name") {
						corrected.Store(true)
						return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"label": "schedule"}}), nil
					}
				}
				return response("admitted"), nil
			}
			if strings.Contains(lastInput(r), "subagent_feedback") {
				return toolResponse("inbox_route", map[string]any{"action": "record"}), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"name": "unregistered-role", "label": "schedule"}}), nil
		}
		workers.Add(1)
		return response("real task executed"), nil
	})
	dir := t.TempDir()
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir, Notify: func(_ context.Context, m mailbox.Message) error { feedback <- m; return nil }})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("schedule request"))
	m := waitFor(t, ctx, feedback)
	if !corrected.Load() || workers.Load() != 1 || m.Meta.TaskID != task || m.Meta.Status != "completed" {
		t.Fatalf("unknown name accepted before repair: corrected=%v workers=%d result=%v", corrected.Load(), workers.Load(), m.Meta)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM tasks").Scan(&n); err != nil || n != 1 {
		t.Fatalf("bad route left durable task: n=%d err=%v", n, err)
	}
}

// Reproduce a real-model failure: reply+task_id was rejected with an ambiguous
// error, so the router retried reply without the intended coordinator target.
// The actual tool boundary must explain how to preserve that handoff instead.
func TestActiveFeedbackHandoffCorrectionPreservesDestination(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	feedback := make(chan mailbox.Message, 8)
	inputs := make(chan string, 8)
	var coordinator string
	var corrected atomic.Bool
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if strings.Contains(lastInput(r), "PRODUCER_EVIDENCE") {
				if r.Messages[len(r.Messages)-1].Role == "tool" {
					for _, part := range r.Messages[len(r.Messages)-1].Content {
						if result := part.GetToolResult(); result != nil && result.IsError {
							if strings.Contains(coretool.ResultText(result), "action=route") {
								corrected.Store(true)
								return toolResponse("inbox_route", map[string]any{"action": "route", "task_id": coordinator}), nil
							}
							return toolResponse("inbox_route", map[string]any{"action": "reply"}), nil
						}
					}
					return response("handoff complete"), nil
				}
				return toolResponse("inbox_route", map[string]any{"action": "reply", "task_id": coordinator}), nil
			}
			if result, ok := activeDecision(r); ok {
				return result, nil
			}
		}
		if lastInput(r) == "producer" {
			return response("PRODUCER_EVIDENCE"), nil
		}
		inputs <- lastInput(r)
		return response("coordinator checked evidence"), nil
	})
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { feedback <- m; return nil }})
	defer closeActive(t, set)
	coordinator = activeSubmit(t, ctx, router, mailbox.NewUserMessage("coordinator"))
	waitFor(t, ctx, inputs)
	waitFor(t, ctx, feedback)
	producer := activeSubmit(t, ctx, router, mailbox.NewUserMessage("producer"))
	m := waitFor(t, ctx, feedback)
	if !corrected.Load() || m.Meta.TaskID != coordinator {
		t.Fatalf("feedback handoff discarded destination: corrected=%v producer=%s feedback=%v", corrected.Load(), producer, m.Meta)
	}
	if got := waitFor(t, ctx, inputs); !strings.Contains(got, "\nPRODUCER_EVIDENCE\n") {
		t.Fatalf("coordinator did not receive original evidence: %q", got)
	}
}

func TestActiveOldExecutionResolvesAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	completed := make(chan mailbox.Message, 4)
	inputs := make(chan *provider.ChatCompletionRequest, 4)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" && strings.Contains(lastInput(r), "historical-address correction") && r.Messages[len(r.Messages)-1].Role != "tool" {
			return toolResponse("inbox_route", map[string]any{"session_id": strings.Split(lastInput(r), "ADDRESS=")[1]}), nil
		}
		if result, router := activeDecision(r); router {
			return result, nil
		}
		inputs <- r
		return response("done"), nil
	})
	config := inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }}
	_, router, _, set := activeHost(t, p, config)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("initial historic task"))
	waitFor(t, ctx, inputs)
	first := waitFor(t, ctx, completed)
	closeActive(t, set)
	f, router, _, reopened := activeHost(t, p, config)
	set = reopened
	defer closeActive(t, set)
	old := first.Meta.ExecutionID
	var misrouted atomic.Int32
	_, err := agenthooks.SessionStart.Emit(ctx, f.Hooks, agenthooks.SessionEvent{SessionID: "unrelated", AgentName: old, Deliver: func(context.Context, mailbox.Message) error { misrouted.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	m := mailbox.NewUserMessage("historical-address correction ADDRESS=" + old)
	m.Meta = mailbox.Metadata{TargetSessionID: old}
	if got := activeSubmit(t, ctx, router, m); got != task {
		t.Fatalf("historical execution created new task: %s", got)
	}
	next := waitFor(t, ctx, inputs)
	if next.SessionID == old {
		t.Fatal("closed execution was reused")
	}
	found := false
	for _, message := range next.Messages {
		found = found || provider.MessageText(message) == "initial historic task"
	}
	if !found {
		t.Fatal("historical execution addressing lost durable history")
	}
	if misrouted.Load() != 0 {
		t.Fatal("live name shadowed a historical execution ID")
	}
	waitFor(t, ctx, completed)
}

func TestActiveAmbiguousNamesStayAmbiguous(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, part := range r.Messages[len(r.Messages)-1].Content {
				if result := part.GetToolResult(); result != nil && result.IsError {
					return toolResponse("inbox_route", map[string]any{"session_id": "second"}), nil
				}
			}
			return response("admitted"), nil
		}
		return toolResponse("inbox_route", map[string]any{"session_id": "duplicate-name"}), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir()})
	defer closeActive(t, set)
	var first, second atomic.Int32
	for _, id := range []string{"first", "second"} {
		_, err := agenthooks.SessionStart.Emit(ctx, f.Hooks, agenthooks.SessionEvent{SessionID: id, AgentName: "duplicate-name", Deliver: func(context.Context, mailbox.Message) error {
			if id == "first" {
				first.Add(1)
			} else {
				second.Add(1)
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := router.Submit(ctx, mailbox.NewUserMessage("explicit ID must recover ambiguity"))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := r.Wait(ctx)
	if err != nil || len(targets) != 1 || targets[0] != "session:second" || first.Load() != 0 || second.Load() != 1 {
		t.Fatalf("ambiguous resolution: targets=%v first=%d second=%d err=%v", targets, first.Load(), second.Load(), err)
	}
}

// A model's record decision cannot make a terminal result disappear. Failed
// transport is retried after restart without reexecuting work.
func TestActiveRecordedCompletionRetriesAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var workers atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("acknowledged"), nil
			}
			if strings.Contains(lastInput(r), "subagent_feedback") {
				return toolResponse("inbox_route", map[string]any{"action": "record"}), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{}}), nil
		}
		workers.Add(1)
		return response("terminal evidence"), nil
	})
	dir := t.TempDir()
	attempted := make(chan struct{}, 4)
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir, Notify: func(context.Context, mailbox.Message) error {
		attempted <- struct{}{}
		return errors.New("requester offline")
	}})
	request := mailbox.NewUserMessage("compute evidence")
	request.Message.Id = "original-request"
	task := activeSubmit(t, ctx, router, request)
	waitFor(t, ctx, attempted)
	closeActive(t, set)
	delivered := make(chan mailbox.Message, 2)
	_, _, _, set = activeHost(t, p, inboxext.Config{StateDir: dir, Notify: func(_ context.Context, m mailbox.Message) error { delivered <- m; return nil }})
	defer closeActive(t, set)
	m := waitFor(t, ctx, delivered)
	if workers.Load() != 1 || m.Meta.TaskID != task || m.Meta.ReplyMessage != request.Message.Id || provider.MessageText(m.Message) != "terminal evidence" {
		t.Fatalf("record lost result or reexecuted work: workers=%d meta=%v", workers.Load(), m.Meta)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM outbox").Scan(&n); err != nil || n != 1 {
		t.Fatalf("outbox count=%d err=%v", n, err)
	}
}

func TestActiveHandoffRecordRetriesAfterRestartWithoutRedispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var workers atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		workers.Add(1)
		return response("worker completed"), nil
	})
	dir := t.TempDir()
	failed := make(chan mailbox.Message, 1)
	completed := make(chan mailbox.Message, 1)
	config := inboxext.Config{StateDir: dir, Record: func(_ context.Context, m mailbox.Message) error {
		failed <- m
		return errors.New("IOA unavailable")
	}, Notify: func(_ context.Context, m mailbox.Message) error {
		completed <- m
		return nil
	}}
	request := mailbox.NewUserMessage("route and record this request")
	request.Message.Id = "handoff-source"
	_, router, _, set := activeHost(t, p, config)
	task := activeSubmit(t, ctx, router, request)
	first := waitFor(t, ctx, failed)
	if first.Meta.Kind != "inbox_handoff" || first.Meta.Record == nil || first.Meta.Record.Target.TaskID != task || first.Meta.MessageID != request.Message.Id {
		t.Fatalf("failed handoff record lost routing identity: %+v", first.Meta)
	}
	if result := waitFor(t, ctx, completed); result.Meta.TaskID != task || result.Meta.Status != "completed" {
		t.Fatalf("worker failed before restart: %+v", result.Meta)
	}
	if workers.Load() != 1 {
		t.Fatalf("worker executions before restart=%d, want 1", workers.Load())
	}
	closeActive(t, set)

	recorded := make(chan mailbox.Message, 2)
	var records atomic.Int32
	config.Record = func(_ context.Context, m mailbox.Message) error {
		records.Add(1)
		recorded <- m
		return nil
	}
	_, router, _, set = activeHost(t, p, config)
	defer closeActive(t, set)
	retry := waitFor(t, ctx, recorded)
	if retry.Message.Id != first.Message.Id || retry.Meta.MessageID != first.Meta.MessageID || retry.Meta.Record == nil || retry.Meta.Record.Target.TaskID != task {
		t.Fatalf("handoff outbox retry changed identity: first=%+v retry=%+v", first, retry)
	}
	if provider.MessageText(retry.Message) != "route and record this request" {
		t.Fatalf("handoff record lost source content: %q", provider.MessageText(retry.Message))
	}
	if got := activeSubmit(t, ctx, router, request); got != task {
		t.Fatalf("duplicate source created task %s, want %s", got, task)
	}
	time.Sleep(100 * time.Millisecond)
	if workers.Load() != 1 || records.Load() != 1 {
		t.Fatalf("replay repeated work or IOA record: workers=%d records=%d", workers.Load(), records.Load())
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sent int
	if err := db.QueryRow("SELECT count(*) FROM outbox WHERE id=? AND state='sent'", retry.Message.Id).Scan(&sent); err != nil || sent != 1 {
		t.Fatalf("handoff outbox sent=%d err=%v", sent, err)
	}
}

func TestActiveFeedbackCycleThroughWorkerName(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("admitted"), nil
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"label": "review-worker"}}), nil
		}
		started <- struct{}{}
		<-callCtx.Done()
		return nil, callCtx.Err()
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir()})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("initial job"))
	waitFor(t, ctx, started)
	p2 := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			for _, c := range r.Messages[len(r.Messages)-1].Content {
				if x := c.GetToolResult(); x != nil && x.IsError {
					return toolResponse("inbox_route", map[string]any{"action": "record"}), nil
				}
			}
			return response("admitted"), nil
		}
		return toolResponse("inbox_route", map[string]any{"session_id": "review-worker"}), nil
	})
	// Switch only the routing provider; the running worker keeps its snapshot.
	// The fixture exposes the same provider publication borrowed by Router.
	f.Providers.Set(p2, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	feedback := mailbox.NewMessage(mailbox.OriginPeer, "user", "echoed result")
	feedback.Meta = mailbox.Metadata{Kind: "task_result", Sender: "peer", FeedbackChain: []string{task}, TargetSessionID: "review-worker"}
	receipt, err := router.Submit(ctx, feedback)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := receipt.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != "record" {
		t.Fatalf("cyclic peer feedback was delivered through worker name: targets=%v", targets)
	}
}
