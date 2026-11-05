package inbox_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
)

func TestActiveFanoutDoesNotAcknowledgeFailedRecipient(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	failed := make(chan struct{}, 1)
	var available atomic.Bool
	var first, second atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			return response("admitted where possible"), nil
		}
		return response("", "first", "second"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir()})
	defer closeActive(t, set)
	for _, id := range []string{"first", "second"} {
		if _, err := agenthooks.SessionStart.Emit(ctx, f.Hooks, agenthooks.SessionEvent{SessionID: id, AgentName: id, Deliver: func(context.Context, mailbox.Message) error {
			if id == "first" {
				first.Add(1)
				return nil
			}
			if !available.Load() {
				select {
				case failed <- struct{}{}:
				default:
				}
				return mailbox.ErrInboxFull
			}
			second.Add(1)
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := router.Submit(ctx, mailbox.NewUserMessage("deliver to both recipients"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ctx, failed)
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	targets, err := receipt.Wait(short)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("partial fanout acknowledged as complete: targets=%v err=%v", targets, err)
	}
	available.Store(true)
	targets, err = receipt.Wait(ctx)
	slices.Sort(targets)
	if err != nil || !reflect.DeepEqual(targets, []string{"session:first", "session:second"}) || first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("fanout retry lost or duplicated admission: targets=%v err=%v first=%d second=%d", targets, err, first.Load(), second.Load())
	}
}

func TestActiveFanoutRestartRetriesOnlyUnacknowledgedRecipient(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	failed := make(chan struct{}, 4)
	var first, second, modelCalls atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		modelCalls.Add(1)
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			return response("admitted where possible"), nil
		}
		return response("", "first", "second"), nil
	})
	message := mailbox.NewUserMessage("restart partial fanout")
	message.Message.Id = "restart-fanout"
	for stage := range 2 {
		f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir})
		closed := false
		t.Cleanup(func() {
			if !closed {
				closeActive(t, set)
			}
		})
		for _, id := range []string{"first", "second"} {
			if _, err := agenthooks.SessionStart.Emit(ctx, f.Hooks, agenthooks.SessionEvent{SessionID: id, Deliver: func(context.Context, mailbox.Message) error {
				if id == "first" {
					first.Add(1)
				} else if stage == 0 {
					failed <- struct{}{}
					return mailbox.ErrInboxFull
				} else {
					second.Add(1)
				}
				return nil
			}}); err != nil {
				t.Fatal(err)
			}
		}
		receipt, err := router.Submit(ctx, message)
		if err != nil {
			t.Fatal(err)
		}
		if stage == 0 {
			waitFor(t, ctx, failed)
			short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			_, err := receipt.Wait(short)
			stop()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("partial fanout acknowledged before restart: %v", err)
			}
		} else {
			targets, err := receipt.Wait(ctx)
			slices.Sort(targets)
			if err != nil || !reflect.DeepEqual(targets, []string{"session:first", "session:second"}) || first.Load() != 1 || second.Load() != 1 || modelCalls.Load() != 2 {
				t.Fatalf("restart lost route or repeated admission: targets=%v err=%v first=%d second=%d modelCalls=%d", targets, err, first.Load(), second.Load(), modelCalls.Load())
			}
		}
		closeActive(t, set)
		closed = true
	}
}

func TestActiveBacklogLargerThanMailbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	requests := make(chan *provider.ChatCompletionRequest, 16)
	completed := make(chan mailbox.Message, 16)
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if strings.Contains(lastInput(r), "hold worker slot") {
			started <- struct{}{}
			select {
			case <-release:
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		} else {
			requests <- r
		}
		return response("batch processed"), nil
	})
	dir := t.TempDir()
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir, MaxActive: 1, Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	db, err := sql.Open("sqlite", filepath.Join(dir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	activeSubmit(t, ctx, router, mailbox.NewUserMessage("hold worker slot"))
	waitFor(t, ctx, started)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("backlog-000"))
	const count = agent.DefaultInboxCapacity + 7
	for i := 1; i < count; i++ {
		m := mailbox.NewUserMessage(fmt.Sprintf("backlog-%03d", i))
		m.Meta = mailbox.Metadata{TaskID: task}
		activeSubmit(t, ctx, router, m)
	}
	close(release)
	seen := make(map[string]bool)
	done := false
	for len(seen) < count || !done {
		select {
		case r := <-requests:
			for _, m := range r.Messages {
				if text := provider.MessageText(m); m.Role == "user" && strings.HasPrefix(text, "backlog-") {
					seen[text] = true
				}
			}
		case m := <-completed:
			if m.Meta.TaskID == task {
				if m.Meta.Status != "completed" {
					t.Fatalf("durable backlog failed before execution: status=%v output=%s", m.Meta, provider.MessageText(m.Message))
				}
				var finished int
				if err := db.QueryRowContext(ctx, "SELECT count(*) FROM inputs WHERE task_id=? AND state='done'", task).Scan(&finished); err != nil {
					t.Fatal(err)
				}
				done = finished == count
			}
		case <-ctx.Done():
			t.Fatalf("only %d/%d durable inputs reached a worker: %v", len(seen), count, ctx.Err())
		}
	}
}

func TestActiveLateUnconsumedCorrectionIsRequeued(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ended, release := make(chan struct{}, 1), make(chan struct{})
	requests := make(chan *provider.ChatCompletionRequest, 4)
	completed := make(chan mailbox.Message, 4)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		requests <- r
		return response("finished"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	var endings atomic.Int32
	agenthooks.RunEnd.On(f.Hooks, "fault.after-last-drain", func(callCtx context.Context, event agenthooks.RunEndEvent) (struct{}, error) {
		if strings.HasPrefix(event.SessionID, "execution-") && endings.Add(1) == 1 {
			ended <- struct{}{}
			select {
			case <-release:
			case <-callCtx.Done():
			}
		}
		return struct{}{}, nil
	})
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("initial request"))
	first := waitFor(t, ctx, requests)
	waitFor(t, ctx, ended)
	m := mailbox.NewUserMessage("arrived after final drain")
	m.Meta = mailbox.Metadata{TaskID: task}
	activeSubmit(t, ctx, router, m)
	close(release)
	next := waitFor(t, ctx, requests)
	if first.SessionID == next.SessionID || !strings.Contains(lastInput(next), "arrived after final drain") {
		t.Fatalf("unconsumed input was acknowledged or misrouted: session=%s input=%s", next.SessionID, lastInput(next))
	}
	for range 2 {
		if m := waitFor(t, ctx, completed); m.Meta.TaskID != task || m.Meta.Status != "completed" {
			t.Fatalf("unexpected feedback: %v", m.Meta)
		}
	}
}

func TestActiveFollowupBeforeWorkerReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	opening, release := make(chan struct{}, 1), make(chan struct{})
	completed := make(chan mailbox.Message, 8)
	requests := make(chan *provider.ChatCompletionRequest, 8)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		requests <- r
		return response("completed"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	var starts atomic.Int32
	agenthooks.SessionStart.On(f.Hooks, "fault.slow-session-open", func(callCtx context.Context, event agenthooks.SessionEvent) (struct{}, error) {
		if strings.HasPrefix(event.SessionID, "execution-") && starts.Add(1) == 1 {
			opening <- struct{}{}
			select {
			case <-release:
			case <-callCtx.Done():
			}
		}
		return struct{}{}, nil
	})
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("initial seed must be processed"))
	waitFor(t, ctx, opening)
	for i := range agent.DefaultInboxCapacity {
		m := mailbox.NewUserMessage(fmt.Sprintf("followup-%03d", i))
		m.Meta = mailbox.Metadata{TaskID: task}
		activeSubmit(t, ctx, router, m)
	}
	close(release)
	for {
		m := waitFor(t, ctx, completed)
		if m.Meta.Status != "completed" {
			t.Fatalf("followups filled mailbox before initial seed: status=%v output=%s", m.Meta, provider.MessageText(m.Message))
		}
		select {
		case r := <-requests:
			for _, message := range r.Messages {
				if provider.MessageText(message) == "initial seed must be processed" {
					return
				}
			}
		default:
		}
	}
}

func TestActiveCompactionDoesNotReplayConsumedInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	started := make(chan string, 8)
	completed := make(chan mailbox.Message, 8)
	var summaries atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 0 {
			summaries.Add(1)
			return response("The diagnostic steps completed; finish this task once."), nil
		}
		if result, router := activeDecision(r); router {
			return result, nil
		}
		started <- r.SessionID
		return response("finished exactly once"), nil
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 32768, MaxTokens: 64})
	// Use the actual controller hook and automatic compactor, with enough
	// observations to remove the original envelope from the retained history.
	agenthooks.BeforeModel.On(f.Hooks, "fault.long-observations", func(_ context.Context, event agenthooks.ContextEvent) ([]*aop.Message, error) {
		if !strings.HasPrefix(event.SessionID, "execution-") {
			return nil, nil
		}
		observations := make([]*aop.Message, 4)
		for i := range observations {
			observations[i] = provider.TextMessage("user", "diagnostic observation: "+strings.Repeat("x", 30000))
		}
		return observations, nil
	})
	activeSubmit(t, ctx, router, mailbox.NewUserMessage("original input: perform diagnostics then finish once"))
	waitFor(t, ctx, started)
	result := waitFor(t, ctx, completed)
	if result.Meta.Status != "completed" || summaries.Load() == 0 {
		t.Fatalf("compaction was not exercised: status=%v summaries=%d output=%s", result.Meta, summaries.Load(), provider.MessageText(result.Message))
	}
	select {
	case next := <-started:
		t.Fatalf("consumed input replayed after context compaction in %s", next)
	case <-time.After(1250 * time.Millisecond):
	}
}

func TestActiveMailboxPressureRetriesDurableCorrections(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first := make(chan struct{}, 1)
	blocked := make(chan struct{}, 1)
	release := make(chan struct{})
	corrected := make(chan struct{}, 1)
	var calls atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		for _, m := range r.Messages {
			if strings.Contains(provider.MessageText(m), "URGENT final correction") {
				corrected <- struct{}{}
				return response("corrected"), nil
			}
		}
		if calls.Add(1) == 1 {
			first <- struct{}{}
			select {
			case <-release:
				return toolResponse("bash", map[string]any{"command": "printf 'continue'"}), nil
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		blocked <- struct{}{}
		<-callCtx.Done()
		return nil, callCtx.Err()
	})
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir()})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("keep working until the final correction"))
	waitFor(t, ctx, first)
	for i := range agent.DefaultInboxCapacity {
		m := mailbox.NewUserMessage("queued observation " + strings.Repeat("x", i%3))
		m.Priority = mailbox.PriorityHigh // Equal priorities force rejection instead of eviction.
		m.Meta = mailbox.Metadata{TaskID: task}
		activeSubmit(t, ctx, router, m)
	}
	last := mailbox.NewUserMessage("URGENT final correction")
	last.Interrupt = true
	last.Priority = mailbox.PriorityHigh
	last.Meta = mailbox.Metadata{TaskID: task}
	activeSubmit(t, ctx, router, last)
	close(release)
	select {
	case <-corrected:
		return
	case <-blocked:
	}
	select {
	case <-corrected:
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("durably accepted interrupt stranded after mailbox capacity became available")
	}
}
