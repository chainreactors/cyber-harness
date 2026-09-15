package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func ptr[T any](v T) *T { return &v }

func TestEmitRunsHandlersInRegistrationOrder(t *testing.T) {
	r := corehooks.New()
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
	r := corehooks.New()
	var calls int
	off := RunEnd.On(r, "counter", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		calls++
		return struct{}{}, nil
	})

	if _, err := RunEnd.Emit(context.Background(), r, RunEndEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	off.Cancel()
	off.Cancel()
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

// Revocation also prevents admission from an old in-flight snapshot.
func TestUnsubscribeDuringDispatch(t *testing.T) {
	r := corehooks.New()
	var seen []string
	var offSecond *corehooks.Subscription

	Context.On(r, "first", func(_ context.Context, _ ContextEvent) (ContextResult, error) {
		seen = append(seen, "first")
		offSecond.Cancel()
		return ContextResult{}, nil
	})
	offSecond = Context.On(r, "second", func(_ context.Context, _ ContextEvent) (ContextResult, error) {
		seen = append(seen, "second")
		return ContextResult{}, nil
	})

	if _, err := Context.Emit(context.Background(), r, ContextEvent{}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Join(seen, ","); got != "first" {
		t.Fatalf("first dispatch = %q, want %q", got, "first")
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
	r := corehooks.New()
	boom := errors.New("boom")
	var secondRan bool
	toolhooks.Before.On(r, "proxy", func(_ context.Context, _ toolhooks.CallEvent) (toolhooks.Admission, error) {
		return toolhooks.Admission{}, boom
	})
	toolhooks.Before.On(r, "audit", func(_ context.Context, _ toolhooks.CallEvent) (toolhooks.Admission, error) {
		secondRan = true
		return toolhooks.Admission{Deny: errors.New("nope")}, nil
	})

	res, err := toolhooks.Before.Emit(context.Background(), r, toolhooks.CallEvent{})
	if err == nil {
		t.Fatal("err = nil, want failure")
	}
	if secondRan {
		t.Fatal("second handler ran after fail-closed abort")
	}
	if res.Deny != nil {
		t.Fatal("result should be zero when dispatch aborts")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is(err, boom) = false: %v", err)
	}

	var he *corehooks.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("errors.As(*corehooks.HandlerError) = false: %v", err)
	}
	if he.Source != "proxy" || he.Kind != "tool.before" {
		t.Fatalf("attribution = %s/%s, want tool_call/proxy", he.Kind, he.Source)
	}
	if got := he.Error(); got != "hook tool.before/proxy: boom" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestHandlerPanicIsAttributedAndReported(t *testing.T) {
	r := corehooks.New()
	toolhooks.Before.On(r, "extension", func(context.Context, toolhooks.CallEvent) (toolhooks.Admission, error) {
		panic("boom")
	})

	_, err := toolhooks.Before.Emit(context.Background(), r, toolhooks.CallEvent{})
	if err == nil {
		t.Fatal("err = nil, want handler panic")
	}
	var reported *corehooks.HandlerError
	if !errors.As(err, &reported) || reported.Source != "extension" || reported.Kind != "tool.before" {
		t.Fatalf("attributed = %+v", reported)
	}
	if reported.Panic != "boom" || len(reported.Stack) == 0 || strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic visibility = %+v, err = %v", reported, err)
	}
}

func TestContinueOnErrorContinuesAfterHandlerPanic(t *testing.T) {
	r := corehooks.New()
	var secondRan bool
	Context.On(r, "extension", func(context.Context, ContextEvent) (ContextResult, error) {
		panic("boom")
	})
	Context.On(r, "core", func(context.Context, ContextEvent) (ContextResult, error) {
		secondRan = true
		return ContextResult{}, nil
	})

	if _, err := Context.Emit(context.Background(), r, ContextEvent{}); err == nil {
		t.Fatal("err = nil, want handler panic")
	}
	if !secondRan {
		t.Fatal("continue-on-error hook stopped after panic")
	}
}

func TestContinueOnErrorCollectsAndKeepsGoing(t *testing.T) {
	r := corehooks.New()
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

func TestToolResultTransformChaining(t *testing.T) {
	r := corehooks.New()
	var observed string
	result := tool.TextResult("raw")

	toolhooks.After.On(r, "redact", func(_ context.Context, ev toolhooks.ResultEvent) (struct{}, error) {
		ev.Result.Output = []*aop.Content{aop.Text(tool.ResultText(ev.Result) + "+redacted")}
		return struct{}{}, nil
	})
	toolhooks.After.On(r, "truncate", func(_ context.Context, ev toolhooks.ResultEvent) (struct{}, error) {
		observed = tool.ResultText(ev.Result)
		ev.Result.IsError = true
		ev.Result.Terminate = true
		return struct{}{}, nil
	})

	_, err := toolhooks.After.Emit(context.Background(), r, toolhooks.ResultEvent{Result: result})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if observed != "raw+redacted" {
		t.Fatalf("second handler saw %q, want the first handler's patch", observed)
	}
	if tool.ResultText(result) != "raw+redacted" {
		t.Fatalf("result output = %q", tool.ResultText(result))
	}
	if !result.IsError {
		t.Fatal("result should be marked as error")
	}
	if !result.Terminate {
		t.Fatal("result should terminate")
	}
}

func TestBeforeRunFoldsSystemPromptAndAggregatesPrepend(t *testing.T) {
	r := corehooks.New()
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
	r := corehooks.New()
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
	r := corehooks.New()
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
	r := corehooks.New()
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
	result toolhooks.Admission
	err    error
)

func TestEmitFastPathDoesNotAllocate(t *testing.T) {
	r := corehooks.New()
	// A handler on a different kind ensures the map lookup misses rather than
	// short-circuiting on an empty table.
	RunEnd.On(r, "other", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		return struct{}{}, nil
	})
	if r.Has("tool.before") {
		t.Fatal("Has(tool_call) = true")
	}

	ctx := context.Background()
	ev := toolhooks.CallEvent{Call: &aop.ToolCall{Id: "c1"}}

	if got := testing.AllocsPerRun(100, func() {
		result, err = toolhooks.Before.Emit(ctx, r, ev)
	}); got != 0 {
		t.Fatalf("Emit allocs = %v, want 0", got)
	}
	if err != nil || result.Deny != nil {
		t.Fatalf("fast path returned %+v, %v", result, err)
	}

	if got := testing.AllocsPerRun(100, func() {
		result, err = toolhooks.Before.Emit(ctx, nil, ev)
	}); got != 0 {
		t.Fatalf("nil-registry Emit allocs = %v, want 0", got)
	}
}

func TestNilRegistryTolerated(t *testing.T) {
	var r *corehooks.Registry
	if r.Has("tool.before") || r.Len("tool.before") != 0 {
		t.Fatal("nil registry reports handlers")
	}
	r.Clear()
	off := toolhooks.Before.On(corehooks.New(), "x", func(_ context.Context, _ toolhooks.CallEvent) (toolhooks.Admission, error) {
		return toolhooks.Admission{}, nil
	})
	off.Cancel()

	res, err := toolhooks.Before.Emit(context.Background(), r, toolhooks.CallEvent{})
	if err != nil || res.Deny != nil {
		t.Fatalf("nil registry Emit = %+v, %v", res, err)
	}
}

func TestOnRequiresSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("On with empty source did not panic")
		}
	}()
	toolhooks.Before.On(corehooks.New(), "", func(_ context.Context, _ toolhooks.CallEvent) (toolhooks.Admission, error) {
		return toolhooks.Admission{}, nil
	})
}

func TestClearDropsHandlers(t *testing.T) {
	r := corehooks.New()
	RunEnd.On(r, "a", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
		return struct{}{}, nil
	})

	r.Clear()
	if r.Has("run_end") {
		t.Fatal("Clear left handlers behind")
	}
}

// Two points sharing a Kind with different types must surface as an attributed
// error rather than a silently skipped handler.
func TestSignatureMismatchIsReported(t *testing.T) {
	r := corehooks.New()
	imposter := corehooks.Point[SessionEvent, struct{}]{Kind: toolhooks.Before.Kind}
	imposter.On(r, "imposter", func(_ context.Context, _ SessionEvent) (struct{}, error) {
		return struct{}{}, nil
	})

	_, err := toolhooks.Before.Emit(context.Background(), r, toolhooks.CallEvent{})
	if !errors.Is(err, corehooks.ErrTypeMismatch) {
		t.Fatalf("err = %v, want type mismatch", err)
	}
}

func TestConcurrentEmitWhileRegistering(t *testing.T) {
	r := corehooks.New()
	var calls atomic.Int64
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
				if _, err := toolhooks.After.Emit(ctx, r, toolhooks.ResultEvent{Result: tool.TextResult("x")}); err != nil {
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
				off := toolhooks.After.On(r, "racer", func(_ context.Context, ev toolhooks.ResultEvent) (struct{}, error) {
					calls.Add(1)
					ev.Result.Output = []*aop.Content{aop.Text(tool.ResultText(ev.Result) + "!")}
					return struct{}{}, nil
				})
				offEnd := RunEnd.On(r, "racer", func(_ context.Context, _ RunEndEvent) (struct{}, error) {
					calls.Add(1)
					return struct{}{}, nil
				})
				off.Cancel()
				offEnd.Cancel()
			}
		}()
	}

	registrars.Wait()
	close(stop)
	emitters.Wait()

	if n := r.Len("tool.after"); n != 0 {
		t.Fatalf("leftover handlers: %d", n)
	}
	if calls.Load() == 0 {
		t.Fatal("no handler ever ran concurrently with registration")
	}
}
