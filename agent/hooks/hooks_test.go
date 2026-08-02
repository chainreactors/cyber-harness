package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
)

func ptr[T any](v T) *T { return &v }

func TestEmitRunsHandlersInRegistrationOrder(t *testing.T) {
	r := New()
	var order []string
	for _, name := range []string{"a", "b", "c"} {
		Context.On(r, name, func(_ context.Context, _ ContextEvent) (ContextResult, error) {
			order = append(order, name)
			return ContextResult{}, nil
		})
	}

	if _, err := Context.Emit(context.Background(), r, ContextEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Join(order, ""); got != "abc" {
		t.Fatalf("order = %q, want %q", got, "abc")
	}
	if n := r.Len("context"); n != 3 {
		t.Fatalf("Len = %d, want 3", n)
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	r := New()
	var calls int
	off := RunEnd.On(r, "counter", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		calls++
		return struct{}{}, nil
	})

	if _, err := RunEnd.Emit(context.Background(), r, RunEndEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	off()
	off()
	if r.Has("run_end") {
		t.Fatal("Has after unsubscribe = true")
	}
	if _, err := RunEnd.Emit(context.Background(), r, RunEndEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// The in-flight dispatch works off an immutable snapshot, so a handler that
// unsubscribes a later handler does not affect the round already running.
func TestUnsubscribeDuringDispatch(t *testing.T) {
	r := New()
	var seen []string
	var offSecond func()

	Context.On(r, "first", func(_ context.Context, _ ContextEvent) (ContextResult, error) {
		seen = append(seen, "first")
		offSecond()
		return ContextResult{}, nil
	})
	offSecond = Context.On(r, "second", func(_ context.Context, _ ContextEvent) (ContextResult, error) {
		seen = append(seen, "second")
		return ContextResult{}, nil
	})

	if _, err := Context.Emit(context.Background(), r, ContextEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Join(seen, ","); got != "first,second" {
		t.Fatalf("first dispatch = %q, want %q", got, "first,second")
	}

	seen = nil
	if _, err := Context.Emit(context.Background(), r, ContextEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Join(seen, ","); got != "first" {
		t.Fatalf("second dispatch = %q, want %q", got, "first")
	}
}

func TestFailClosedShortCircuits(t *testing.T) {
	r := New()
	var sunk []*HandlerError
	r.SetErrorSink(func(he *HandlerError) { sunk = append(sunk, he) })

	boom := errors.New("boom")
	var secondRan bool
	ToolCallHook.On(r, "proxy", func(_ context.Context, _ ToolCallEvent) (ToolCallResult, error) {
		return ToolCallResult{}, boom
	})
	ToolCallHook.On(r, "audit", func(_ context.Context, _ ToolCallEvent) (ToolCallResult, error) {
		secondRan = true
		return ToolCallResult{Block: true, Reason: "nope"}, nil
	})

	res, err := ToolCallHook.Emit(context.Background(), r, ToolCallEvent{})
	if err == nil {
		t.Fatal("err = nil, want failure")
	}
	if secondRan {
		t.Fatal("second handler ran after fail-closed abort")
	}
	if res.Block {
		t.Fatal("result should be zero when dispatch aborts")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is(err, boom) = false: %v", err)
	}

	var he *HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("errors.As(*HandlerError) = false: %v", err)
	}
	if he.Source != "proxy" || he.Kind != "tool_call" {
		t.Fatalf("attribution = %s/%s, want tool_call/proxy", he.Kind, he.Source)
	}
	if got := he.Error(); got != "hook tool_call/proxy: boom" {
		t.Fatalf("Error() = %q", got)
	}
	if len(sunk) != 1 || sunk[0] != he {
		t.Fatalf("sink got %d errors, want the one reported", len(sunk))
	}
}

func TestContinueOnErrorCollectsAndKeepsGoing(t *testing.T) {
	r := New()
	first := errors.New("first")
	second := errors.New("second")
	var ran int

	for _, tc := range []struct {
		source string
		err    error
	}{{"a", first}, {"b", second}, {"c", nil}} {
		RunEnd.On(r, tc.source, func(_ context.Context, _ RunEndEvent) (struct{}, error) {
			ran++
			return struct{}{}, tc.err
		})
	}

	_, err := RunEnd.Emit(context.Background(), r, RunEndEvent{})
	if ran != 3 {
		t.Fatalf("ran = %d, want 3", ran)
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("err = %v, want both collected", err)
	}
}

func TestToolResultPatchChaining(t *testing.T) {
	r := New()
	var observed string

	ToolResult.On(r, "redact", func(_ context.Context, ev ToolResultEvent) (ToolResultPatch, error) {
		return ToolResultPatch{Content: ptr(ev.Content + "+redacted")}, nil
	})
	ToolResult.On(r, "truncate", func(_ context.Context, ev ToolResultEvent) (ToolResultPatch, error) {
		observed = ev.Content
		return ToolResultPatch{IsError: ptr(true), Terminate: ptr(true)}, nil
	})

	patch, err := ToolResult.Emit(context.Background(), r, ToolResultEvent{Content: "raw"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if observed != "raw+redacted" {
		t.Fatalf("second handler saw %q, want the first handler's patch", observed)
	}
	if patch.Content == nil || *patch.Content != "raw+redacted" {
		t.Fatalf("patch.Content = %v", patch.Content)
	}
	if patch.IsError == nil || !*patch.IsError {
		t.Fatalf("patch.IsError = %v", patch.IsError)
	}
	if patch.Terminate == nil || !*patch.Terminate {
		t.Fatalf("patch.Terminate = %v", patch.Terminate)
	}
}

func TestBeforeRunFoldsSystemPromptAndAggregatesPrepend(t *testing.T) {
	r := New()
	var observed string

	BeforeRun.On(r, "base", func(_ context.Context, ev RunStartEvent) (RunStartResult, error) {
		return RunStartResult{
			SystemPrompt: ptr(ev.SystemPrompt + "\nbase"),
			Prepend:      []*Msg{{Role: "system", Content: []*aop.Content{aop.Text("one")}}},
		}, nil
	})
	BeforeRun.On(r, "extra", func(_ context.Context, ev RunStartEvent) (RunStartResult, error) {
		observed = ev.SystemPrompt
		return RunStartResult{
			SystemPrompt: ptr(ev.SystemPrompt + "\nextra"),
			Prepend:      []*Msg{{Role: "user", Content: []*aop.Content{aop.Text("two")}}},
		}, nil
	})

	res, err := BeforeRun.Emit(context.Background(), r, RunStartEvent{SystemPrompt: "root"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if observed != "root\nbase" {
		t.Fatalf("second handler saw %q, want the folded prompt", observed)
	}
	if res.SystemPrompt == nil || *res.SystemPrompt != "root\nbase\nextra" {
		t.Fatalf("SystemPrompt = %v", res.SystemPrompt)
	}
	if len(res.Prepend) != 2 || res.Prepend[0].Role != "system" || res.Prepend[1].Role != "user" {
		t.Fatalf("Prepend = %+v", res.Prepend)
	}
}

func TestContextReplacementFolds(t *testing.T) {
	r := New()
	var observed int

	Context.On(r, "drop", func(_ context.Context, ev ContextEvent) (ContextResult, error) {
		return ContextResult{Messages: ev.Messages[1:]}, nil
	})
	Context.On(r, "noop", func(_ context.Context, ev ContextEvent) (ContextResult, error) {
		observed = len(ev.Messages)
		return ContextResult{}, nil
	})

	res, err := Context.Emit(context.Background(), r, ContextEvent{Messages: make([]*Msg, 3)})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if observed != 2 {
		t.Fatalf("second handler saw %d messages, want 2", observed)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("result = %d messages, want 2", len(res.Messages))
	}
}

func TestStopWhenShortCircuits(t *testing.T) {
	r := New()
	var ran int

	BeforeCompact.On(r, "budget", func(_ context.Context, _ CompactEvent) (CancelResult, error) {
		ran++
		return CancelResult{Cancel: true, Reason: "still cheap"}, nil
	})
	BeforeCompact.On(r, "never", func(_ context.Context, _ CompactEvent) (CancelResult, error) {
		ran++
		return CancelResult{}, nil
	})

	res, err := BeforeCompact.Emit(context.Background(), r, CompactEvent{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if ran != 1 {
		t.Fatalf("ran = %d, want 1", ran)
	}
	if !res.Cancel || res.Reason != "still cheap" {
		t.Fatalf("res = %+v", res)
	}
}

func TestObservationPointsIgnoreResults(t *testing.T) {
	r := New()
	var ran int
	for _, name := range []string{"a", "b"} {
		SessionStart.On(r, name, func(_ context.Context, _ SessionEvent) (struct{}, error) {
			ran++
			return struct{}{}, nil
		})
	}

	res, err := SessionStart.Emit(context.Background(), r, SessionEvent{SessionID: "s1"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if ran != 2 {
		t.Fatalf("ran = %d, want 2 (observation must not short-circuit)", ran)
	}
	if res != (struct{}{}) {
		t.Fatal("observation result must be zero")
	}
}

var (
	sinkResult ToolCallResult
	sinkErr    error
)

func TestEmitFastPathDoesNotAllocate(t *testing.T) {
	r := New()
	// A handler on a different kind ensures the map lookup misses rather than
	// short-circuiting on an empty table.
	RunEnd.On(r, "other", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		return struct{}{}, nil
	})
	if r.Has("tool_call") {
		t.Fatal("Has(tool_call) = true")
	}

	ctx := context.Background()
	ev := ToolCallEvent{SessionID: "s1", TurnID: "t1", Call: &ToolCall{Id: "c1"}}

	if got := testing.AllocsPerRun(100, func() {
		sinkResult, sinkErr = ToolCallHook.Emit(ctx, r, ev)
	}); got != 0 {
		t.Fatalf("Emit allocs = %v, want 0", got)
	}
	if sinkErr != nil || sinkResult.Block {
		t.Fatalf("fast path returned %+v, %v", sinkResult, sinkErr)
	}

	if got := testing.AllocsPerRun(100, func() {
		sinkResult, sinkErr = ToolCallHook.Emit(ctx, nil, ev)
	}); got != 0 {
		t.Fatalf("nil-registry Emit allocs = %v, want 0", got)
	}
}

func TestNilRegistryTolerated(t *testing.T) {
	var r *Registry
	if r.Has("tool_call") || r.Len("tool_call") != 0 {
		t.Fatal("nil registry reports handlers")
	}
	r.SetErrorSink(func(*HandlerError) {})
	r.Clear()
	off := ToolCallHook.On(r, "x", func(_ context.Context, _ ToolCallEvent) (ToolCallResult, error) {
		return ToolCallResult{}, nil
	})
	off()

	res, err := ToolCallHook.Emit(context.Background(), r, ToolCallEvent{})
	if err != nil || res.Block {
		t.Fatalf("nil registry Emit = %+v, %v", res, err)
	}
}

func TestOnRequiresSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("On with empty source did not panic")
		}
	}()
	ToolCallHook.On(New(), "", func(_ context.Context, _ ToolCallEvent) (ToolCallResult, error) {
		return ToolCallResult{}, nil
	})
}

func TestClearDropsHandlersAndRunsCleanups(t *testing.T) {
	r := New()
	RunEnd.On(r, "a", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		return struct{}{}, nil
	})

	var order []string
	r.AddCleanup(func() { order = append(order, "first") })
	removeSecond := r.AddCleanup(func() { order = append(order, "second") })
	r.AddCleanup(func() { order = append(order, "third") })
	removeSecond()
	removeSecond()

	r.Clear()
	if r.Has("run_end") {
		t.Fatal("Clear left handlers behind")
	}
	if got := strings.Join(order, ","); got != "first,third" {
		t.Fatalf("cleanups = %q, want %q", got, "first,third")
	}

	r.Clear()
	if len(order) != 2 {
		t.Fatalf("cleanups ran twice: %v", order)
	}
}

// Two points sharing a Kind with different types must surface as an attributed
// error rather than a silently skipped handler.
func TestSignatureMismatchIsReported(t *testing.T) {
	r := New()
	imposter := Point[SessionEvent, struct{}]{Kind: ToolCallHook.Kind}
	imposter.On(r, "imposter", func(_ context.Context, _ SessionEvent) (struct{}, error) {
		return struct{}{}, nil
	})

	_, err := ToolCallHook.Emit(context.Background(), r, ToolCallEvent{})
	if !errors.Is(err, errTypeMismatch) {
		t.Fatalf("err = %v, want type mismatch", err)
	}
}

func TestConcurrentEmitWhileRegistering(t *testing.T) {
	r := New()
	var calls atomic.Int64
	r.SetErrorSink(func(*HandlerError) {})

	ctx := context.Background()
	stop := make(chan struct{})
	var emitters, registrars sync.WaitGroup

	for i := 0; i < 8; i++ {
		emitters.Add(1)
		go func() {
			defer emitters.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := ToolResult.Emit(ctx, r, ToolResultEvent{Content: "x"}); err != nil {
					t.Errorf("emit: %v", err)
					return
				}
				_, _ = RunEnd.Emit(ctx, r, RunEndEvent{Stop: StopReasonCompleted})
			}
		}()
	}

	for i := 0; i < 4; i++ {
		registrars.Add(1)
		go func() {
			defer registrars.Done()
			for j := 0; j < 200; j++ {
				off := ToolResult.On(r, "racer", func(_ context.Context, ev ToolResultEvent) (ToolResultPatch, error) {
					calls.Add(1)
					return ToolResultPatch{Content: ptr(ev.Content + "!")}, nil
				})
				offEnd := RunEnd.On(r, "racer", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
					calls.Add(1)
					return struct{}{}, nil
				})
				off()
				offEnd()
			}
		}()
	}

	registrars.Wait()
	close(stop)
	emitters.Wait()

	if n := r.Len("tool_result"); n != 0 {
		t.Fatalf("leftover handlers: %d", n)
	}
	if calls.Load() == 0 {
		t.Fatal("no handler ever ran concurrently with registration")
	}
}
