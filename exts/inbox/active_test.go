package inbox_test

import (
	"bytes"
	"context"
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

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	"github.com/chainreactors/utils/proc"
)

func toolResponse(name string, args any) *provider.ChatCompletionResponse {
	b, _ := json.Marshal(args)
	return &provider.ChatCompletionResponse{Choices: []provider.Choice{{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: aop.EnvelopeID(), Name: name, Arguments: &aop.EncodedValue{Data: b, MediaType: aop.JSONMediaType}}}}}}}}}
}

func activeDecision(request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, bool) {
	if len(request.Tools) != 1 || request.Tools[0].Name != "inbox_route" {
		return nil, false
	}
	if request.Messages[len(request.Messages)-1].Role == "tool" {
		return response("admitted"), true
	}
	input := lastInput(request)
	metaText := strings.Split(strings.TrimPrefix(input, "Current routing context:\n"), "\nOriginal message:\n")[0]
	var routing struct {
		Meta mailbox.Metadata `json:"meta"`
	}
	if err := json.Unmarshal([]byte(metaText), &routing); err != nil {
		return response("invalid routing metadata: " + err.Error()), true
	}
	if routing.Meta.Kind == "subagent_feedback" {
		return toolResponse("inbox_route", map[string]any{"action": "reply"}), true
	}
	if task := routing.Meta.TaskID; task != "" {
		return toolResponse("inbox_route", map[string]any{"task_id": task}), true
	}
	return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{}}), true
}

func activeHost(t *testing.T, p provider.Provider, config inboxext.Config, extra ...extension.Extension) (*apptest.Fixture, *inboxext.Router, *session.Runtime, *extension.Set) {
	t.Helper()
	f := apptest.NewFixture(t, nil, nil)
	f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
	router := inboxext.New(config)
	sessions := sessionext.New(session.Config{})
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), sessions, subagentext.NewTools(), router)
	entries = append(entries, extra...)
	set := hosttest.Load(t, t.Context(), entries...)
	return f, router.Router(), sessions.Runtime(), set
}

func closeActive(t *testing.T, set *extension.Set) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := set.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func activeSubmit(t *testing.T, ctx context.Context, router *inboxext.Router, message mailbox.Message) string {
	t.Helper()
	r, err := router.Submit(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := r.Wait(ctx)
	if err != nil || len(targets) != 1 || !strings.HasPrefix(targets[0], "task:") {
		t.Fatalf("routing=%v error=%v", targets, err)
	}
	return strings.TrimPrefix(targets[0], "task:")
}

func TestActiveHistoryDedupAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	completed := make(chan mailbox.Message, 8)
	requests := make(chan *provider.ChatCompletionRequest, 8)
	p := modelFunc(func(_ context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(request); router {
			return result, nil
		}
		requests <- request
		return response("finished: " + lastInput(request)), nil
	})
	config := inboxext.Config{StateDir: dir, Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }}
	f, router, _, set := activeHost(t, p, config)
	initial := mailbox.NewUserMessage("private alpha task")
	initial.Message.Id = "initial"
	task := activeSubmit(t, ctx, router, initial)
	first := waitFor(t, ctx, requests)
	if m := waitFor(t, ctx, completed); m.Meta.TaskID != task {
		t.Fatalf("wrong feedback: %v", m.Meta)
	}
	if err := router.SaveCursor("test-feed", "cursor-7"); err != nil {
		t.Fatal(err)
	}
	var hosted bool
	for _, info := range f.Shell.Manager().List() {
		hosted = hosted || info.Kind == "agent" && info.Name == "router" && info.State.Active() && info.ProcessID() == 0
	}
	if !hosted {
		t.Fatal("router is not hosted by the internal work-unit manager")
	}
	closeActive(t, set)
	_, router, _, set = activeHost(t, p, config)
	defer closeActive(t, set)
	if cursor, err := router.LoadCursor("test-feed"); cursor != "cursor-7" || err != nil {
		t.Fatalf("cursor=%q error=%v", cursor, err)
	}
	if duplicate := activeSubmit(t, ctx, router, initial); duplicate != task {
		t.Fatalf("duplicate task %s != %s", duplicate, task)
	}
	next := mailbox.NewUserMessage("continue alpha")
	next.Message.Id = "initial" // scoped IDs from different senders are distinct inputs
	next.Meta = mailbox.Metadata{TaskID: task, Sender: "second-source"}
	activeSubmit(t, ctx, router, next)
	restored := waitFor(t, ctx, requests)
	if restored.SessionID == first.SessionID {
		t.Fatal("completed execution was reused")
	}
	if !strings.Contains(fmt.Sprint(restored.Messages), "private alpha task") {
		t.Fatal("restored task lost its own transcript")
	}
	waitFor(t, ctx, completed)
	other := activeSubmit(t, ctx, router, mailbox.NewUserMessage("independent beta"))
	if other == task {
		t.Fatal("independent task was merged")
	}
	isolated := waitFor(t, ctx, requests)
	for _, m := range isolated.Messages {
		if strings.Contains(provider.MessageText(m), "private alpha") {
			t.Fatal("task history leaked")
		}
	}
	waitFor(t, ctx, completed)
	select {
	case unexpected := <-requests:
		t.Fatalf("duplicate was executed: %v", unexpected)
	default:
	}
}

func TestActiveBoundedConcurrencyAndCorrection(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	started := make(chan string, 8)
	var calls atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		started <- lastInput(r)
		if calls.Add(1) == 1 {
			<-callCtx.Done()
			return nil, callCtx.Err()
		}
		return response("finished"), nil
	})
	completed := make(chan mailbox.Message, 8)
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), MaxActive: 1, Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("long first"))
	waitFor(t, ctx, started)
	activeSubmit(t, ctx, router, mailbox.NewUserMessage("independent second"))
	select {
	case text := <-started:
		t.Fatalf("concurrency bound violated: %s", text)
	case <-time.After(100 * time.Millisecond):
	}
	correction := mailbox.NewUserMessage("correct the first task")
	correction.Interrupt = true
	correction.Priority = mailbox.PriorityHigh
	correction.Meta = mailbox.Metadata{TaskID: task}
	activeSubmit(t, ctx, router, correction)
	if text := waitFor(t, ctx, started); !strings.Contains(text, "correct the first") {
		t.Fatalf("correction=%s", text)
	}
	waitFor(t, ctx, completed)
	if text := waitFor(t, ctx, started); !strings.Contains(text, "independent second") {
		t.Fatalf("queued task=%s", text)
	}
	waitFor(t, ctx, completed)
}

func TestActiveRetryFairnessAndFeedbackCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var workers, feedbackAttempts atomic.Int32
	completed := make(chan mailbox.Message, 2)
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route" {
			if strings.Contains(lastInput(r), "POISON") {
				return nil, errors.New("injected routing failure")
			}
			if strings.Contains(lastInput(r), `"kind":"subagent_feedback"`) {
				if feedbackAttempts.Add(1) == 1 {
					text := strings.Split(strings.TrimPrefix(lastInput(r), "Current routing context:\n"), "\nOriginal message:\n")[0]
					var value struct {
						Meta mailbox.Metadata `json:"meta"`
					}
					_ = json.Unmarshal([]byte(text), &value)
					return toolResponse("inbox_route", map[string]any{"task_id": value.Meta.TaskID}), nil
				}
				if r.Messages[len(r.Messages)-1].Role == "tool" && feedbackAttempts.Load() == 2 {
					return toolResponse("inbox_route", map[string]any{"action": "reply"}), nil
				}
			}
			result, _ := activeDecision(r)
			return result, nil
		}
		workers.Add(1)
		return response("healthy result"), nil
	})
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	poison, err := router.Submit(ctx, mailbox.NewUserMessage("POISON"))
	if err != nil {
		t.Fatal(err)
	}
	activeSubmit(t, ctx, router, mailbox.NewUserMessage("healthy independent request"))
	waitFor(t, ctx, completed)
	if workers.Load() != 1 || feedbackAttempts.Load() < 2 {
		t.Fatalf("feedback cycle was executed: workers=%d feedback=%d", workers.Load(), feedbackAttempts.Load())
	}
	short, stop := context.WithTimeout(ctx, 25*time.Millisecond)
	defer stop()
	if _, err := poison.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("poison receipt=%v", err)
	}
}

func TestActiveCancellationStopsOwnedBackgroundJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	background := make(chan struct{}, 1)
	var calls atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		if calls.Add(1) == 1 {
			return toolResponse("bash", map[string]any{"command": "sleep 30", "wait": 1, "timeout": 0}), nil
		}
		background <- struct{}{}
		<-callCtx.Done()
		return nil, callCtx.Err()
	})
	completed := make(chan mailbox.Message, 2)
	f, router, rt, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("run long background work"))
	waitFor(t, ctx, background)
	var unitID string
	for _, info := range f.Shell.Manager().List() {
		if info.Name == task && info.State.Active() {
			unitID = info.ID
		}
	}
	if unitID == "" {
		t.Fatal("no managed execution")
	}
	if err := f.Shell.Manager().Stop(unitID, proc.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Shell.Manager().Wait(ctx, unitID, 0); err != nil {
		t.Fatal(err)
	}
	result := waitFor(t, ctx, completed)
	if result.Meta.Status != "canceled" {
		t.Fatalf("status=%v", result.Meta)
	}
	for _, info := range f.Shell.Manager().List() {
		if info.Name != "router" && info.State.Active() {
			t.Fatalf("execution left owned process running: %+v", info)
		}
	}
	if _, err := rt.RunSession(ctx, result.Meta.ExecutionID, session.RunInput{Continue: true}); err == nil {
		t.Fatal("finished execution was not released")
	}
}

func TestActiveTmuxWorkerFollowupAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	requests := make(chan *provider.ChatCompletionRequest, 4)
	admitted := make(chan struct{}, 1)
	release := make(chan struct{})
	completed := make(chan mailbox.Message, 4)
	var calls atomic.Int32
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			if r.Messages[len(r.Messages)-1].Role == "tool" && strings.Contains(lastInput(r), "operator followup") {
				admitted <- struct{}{}
			}
			return result, nil
		}
		requests <- r
		if calls.Add(1) == 1 {
			select {
			case <-release:
				return response("initial step finished"), nil
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		<-callCtx.Done()
		return nil, callCtx.Err()
	})
	f, router, _, set := activeHost(t, p, inboxext.Config{StateDir: t.TempDir(), Notify: func(_ context.Context, m mailbox.Message) error { completed <- m; return nil }})
	defer closeActive(t, set)
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("tmux initial task"))
	waitFor(t, ctx, requests)
	var unitID string
	for _, info := range f.Shell.Manager().List() {
		if info.Name == task && info.State.Active() {
			unitID = info.ID
		}
	}
	if unitID == "" {
		t.Fatal("worker was not hosted")
	}
	args, _ := json.Marshal(map[string]any{"command": "tmux send-keys -t " + unitID + " 'operator followup' Enter"})
	result, err := f.Shell.Execute(ctx, string(args))
	if err != nil || !strings.Contains(coretool.ResultText(result), "sent") {
		t.Fatalf("tmux worker input: %v: %s", err, coretool.ResultText(result))
	}
	waitFor(t, ctx, admitted)
	close(release)
	followup := waitFor(t, ctx, requests)
	if !strings.Contains(lastInput(followup), "operator followup") {
		t.Fatalf("worker did not consume operator input: %s", lastInput(followup))
	}
	var history bool
	for _, m := range followup.Messages {
		history = history || strings.Contains(provider.MessageText(m), "tmux initial task")
	}
	if !history {
		t.Fatal("operator input lost the original task context")
	}
	unitID = ""
	for _, info := range f.Shell.Manager().List() {
		if info.Name == task && info.State.Active() {
			unitID = info.ID
		}
	}
	if unitID == "" {
		t.Fatal("follow-up did not remain on the original task")
	}
	args, _ = json.Marshal(map[string]any{"command": "tmux kill -t " + unitID})
	result, err = f.Shell.Execute(ctx, string(args))
	if err != nil || !strings.Contains(coretool.ResultText(result), "killed") {
		t.Fatalf("tmux worker stop: %v: %s", err, coretool.ResultText(result))
	}
	for {
		feedback := waitFor(t, ctx, completed)
		if feedback.Meta.TaskID != task {
			t.Fatalf("operator created an unrelated task: %v", feedback.Meta)
		}
		if feedback.Meta.Status == "canceled" {
			break
		}
	}
	if _, err := f.Shell.Manager().Wait(ctx, unitID, 0); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected worker calls: %d", calls.Load())
	}
}

func TestActiveOutboxSurvivesRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	attempt := make(chan struct{}, 2)
	var workers atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		workers.Add(1)
		return response("durable result"), nil
	})
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: dir, Notify: func(context.Context, mailbox.Message) error {
		attempt <- struct{}{}
		return errors.New("destination offline")
	}})
	task := activeSubmit(t, ctx, router, mailbox.NewUserMessage("persist result"))
	waitFor(t, ctx, attempt)
	closeActive(t, set)
	delivered := make(chan mailbox.Message, 1)
	_, _, _, set = activeHost(t, p, inboxext.Config{StateDir: dir, Notify: func(_ context.Context, m mailbox.Message) error { delivered <- m; return nil }})
	defer closeActive(t, set)
	m := waitFor(t, ctx, delivered)
	if workers.Load() != 1 || m.Meta.TaskID != task || provider.MessageText(m.Message) != "durable result" {
		t.Fatalf("reexecuted task or lost result: workers=%d result=%+v", workers.Load(), m)
	}
}

// A real process is killed after a side effect and its canonical tool result,
// then a new host restores the same task. No external model is involved.
func TestActiveCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("process crash integration")
	}
	for _, origin := range []mailbox.Origin{mailbox.OriginUser, mailbox.OriginPeer, mailbox.OriginSystem, mailbox.OriginSession} {
		t.Run(string(origin), func(t *testing.T) {
			dir := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			start := func(stage string) (*exec.Cmd, *bytes.Buffer) {
				cmd := exec.Command(executable, "-test.run=^TestActiveCrashProcess$", "-test.timeout=30s")
				cmd.Env = append(os.Environ(), "CYBER_ACTIVE_CRASH_DIR="+dir, "CYBER_ACTIVE_CRASH_STAGE="+stage, "CYBER_ACTIVE_CRASH_ORIGIN="+string(origin))
				out := new(bytes.Buffer)
				cmd.Stdout = out
				cmd.Stderr = out
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				return cmd, out
			}
			cmd, out := start("before")
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			deadline := time.Now().Add(15 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("child never reached durable boundary: %s", out.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			cmd, out = start("after")
			if err := cmd.Wait(); err != nil {
				t.Fatalf("restart failed: %v\n%s", err, out.String())
			}
			b, err := os.ReadFile(filepath.Join(dir, "effects"))
			if err != nil || string(b) != "effect\n" {
				t.Fatalf("side effect repeated or lost: %q %v", b, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "recovered")); err != nil {
				t.Fatal("task result was not returned after recovery")
			}
		})
	}
}

func TestActiveCrashProcess(t *testing.T) {
	dir := os.Getenv("CYBER_ACTIVE_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess only")
	}
	stage := os.Getenv("CYBER_ACTIVE_CRASH_STAGE")
	done := make(chan mailbox.Message, 1)
	p := modelFunc(func(ctx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		hasResult, inputs := false, 0
		inputIndex, resultIndex := -1, -1
		for i, m := range r.Messages {
			if m.Role == "user" && strings.Contains(provider.MessageText(m), "perform side effect once and then finish") {
				inputs++
				inputIndex = i
			}
			for _, part := range m.Content {
				if tr := part.GetToolResult(); tr != nil && tr.Name == "bash" {
					hasResult = true
					resultIndex = i
				}
			}
		}
		if stage == "after" {
			if !hasResult || inputs != 1 || inputIndex >= resultIndex || !strings.Contains(lastInput(r), "Execution was interrupted") {
				return nil, fmt.Errorf("recovery missing journal, input order, input dedup, or recovery instruction: result=%v inputs=%d inputIndex=%d resultIndex=%d", hasResult, inputs, inputIndex, resultIndex)
			}
			return response("recovered result"), nil
		}
		if !hasResult {
			path := strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, "effects")), "'", "'\"'\"'")
			return toolResponse("bash", map[string]any{"command": "printf 'effect\\n' >> '" + path + "'"}), nil
		}
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: filepath.Join(dir, "state"), Notify: func(_ context.Context, m mailbox.Message) error { done <- m; return nil }})
	defer closeActive(t, set)
	if stage == "before" {
		m := mailbox.NewUserMessage("perform side effect once and then finish")
		m.Origin = mailbox.Origin(os.Getenv("CYBER_ACTIVE_CRASH_ORIGIN"))
		m.Message.Id = "crash-request"
		activeSubmit(t, t.Context(), router, m)
		<-t.Context().Done()
	} else {
		m := waitFor(t, t.Context(), done)
		if !strings.Contains(provider.MessageText(m.Message), "recovered result") {
			t.Fatalf("recovery failed: %s", provider.MessageText(m.Message))
		}
		if err := os.WriteFile(filepath.Join(dir, "recovered"), []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
