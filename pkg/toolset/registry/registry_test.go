package registry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

type testTool struct {
	def *tool.Definition
	run func(context.Context, string) (*tool.Result, error)
}

func newTool(name string) *testTool {
	return &testTool{def: tool.Def(name, "test", struct{}{})}
}
func (t *testTool) Name() string                 { return t.def.Name }
func (t *testTool) Description() string          { return t.def.Description }
func (t *testTool) Definition() *tool.Definition { return t.def }
func (t *testTool) Execute(ctx context.Context, args string) (*tool.Result, error) {
	if t.run != nil {
		return t.run(ctx, args)
	}
	return tool.TextResult(args), nil
}

func registrySet(t *testing.T, r *registry.Registry) *extension.Set {
	t.Helper()
	s, err := extension.New(extension.Entry{ID: "registry", Extension: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func activeRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r := registry.New()
	s := registrySet(t, r)
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExplicitLoadAndContextOwnership(t *testing.T) {
	r := registry.New()
	s := registrySet(t, r)
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("new registry publishes definitions")
	}
	if _, err := r.Register("one", newTool("echo")); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("register before load: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load: %v", err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	checked := newTool("echo")
	checked.run = func(ctx context.Context, args string) (*tool.Result, error) {
		if got := tool.InvocationFromContext(ctx); got.CallID != "call-1" || got.SessionID != "" {
			t.Errorf("invocation was not preserved: %+v", got)
		}
		return tool.TextResult(args), ctx.Err()
	}
	if _, err := r.Register("one", checked); err != nil {
		t.Fatal(err)
	}
	callCtx := tool.ContextWithInvocation(t.Context(), tool.Invocation{CallID: "call-1"})
	result, err := r.ExecuteTool(callCtx, "echo", "value")
	if err != nil || tool.ResultText(result) != "value" {
		t.Fatalf("load context canceled the instance: %v, %v", result, err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(t.Context()); err == nil {
		t.Fatalf("closed instance reloaded: %v", err)
	}
}

func TestAtomicRegistrationAndDefinitionSnapshots(t *testing.T) {
	r := activeRegistry(t)
	first := newTool("first")
	if _, err := r.Register("original", first); err != nil {
		t.Fatal(err)
	}
	if lease, err := r.Register("conflict", newTool("second"), newTool("first")); !errors.Is(err, registry.ErrDuplicate) || lease != nil {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := r.Register("original", newTool("third")); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate owner: %v", err)
	}
	if _, err := r.Register("batch", newTool("fourth"), newTool("fourth")); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate in batch: %v", err)
	}
	first.def.Description = "changed source"
	defs := r.ToolDefinitions()
	if len(defs) != 1 || defs[0].Description != "test" {
		t.Fatalf("partial publication or shared source definition: %v", defs)
	}
	defs[0].Name = "changed snapshot"
	defs[0].InputSchema = nil
	if got := r.ToolDefinitions()[0]; got.Name != "first" || got.InputSchema == nil {
		t.Fatalf("mutable discovery changed registration: %v", got)
	}
	if _, err := r.ExecuteTool(t.Context(), "second", ""); !errors.Is(err, registry.ErrUnknown) {
		t.Fatalf("failed group left callable tool: %v", err)
	}
	if _, err := r.ExecuteTool(t.Context(), "first", ""); err != nil {
		t.Fatalf("collision affected original owner: %v", err)
	}
}

func TestOwnerTimeoutRetainsAdmissionBarrierAndCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := activeRegistry(t)
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		blocked := newTool("blocked")
		blocked.run = func(ctx context.Context, _ string) (*tool.Result, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release // deliberately needs more time to release its resource
			return nil, ctx.Err()
		}
		lease, err := r.Register("busy", blocked)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Register("other", newTool("echo")); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() {
			_, err := r.ExecuteTool(t.Context(), "blocked", "")
			finished <- err
		}()
		<-started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := lease.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout: %v", err)
		}
		<-canceled
		if defs := r.ToolDefinitions(); len(defs) != 1 || defs[0].Name != "echo" {
			t.Fatalf("stopping owner's discovery: %v", defs)
		}
		if _, err := r.ExecuteTool(t.Context(), "blocked", ""); !errors.Is(err, registry.ErrUnavailable) {
			t.Fatalf("accepted work during shutdown: %v", err)
		}
		if _, err := r.ExecuteTool(t.Context(), "echo", ""); err != nil {
			t.Fatalf("stopped unrelated owner: %v", err)
		}
		if _, err := r.Register("busy", newTool("replacement")); !errors.Is(err, registry.ErrDuplicate) {
			t.Fatalf("reused stopping owner: %v", err)
		}
		unblock()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("inflight cancellation: %v", err)
		}
		if err := lease.Close(t.Context()); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if _, err := r.Register("new-owner", newTool("blocked")); !errors.Is(err, registry.ErrDuplicate) {
			t.Fatalf("reused retired name: %v", err)
		}
	})
}

func TestRequestCancellationDoesNotStopOwner(t *testing.T) {
	r := activeRegistry(t)
	started := make(chan struct{})
	work := newTool("work")
	work.run = func(ctx context.Context, args string) (*tool.Result, error) {
		if args == "wait" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return tool.TextResult("ready"), nil
	}
	if _, err := r.Register("work", work); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := r.ExecuteTool(ctx, "work", "wait")
		finished <- err
	}()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if result, err := r.ExecuteTool(t.Context(), "work", "again"); err != nil || tool.ResultText(result) != "ready" {
		t.Fatalf("request cancellation stopped owner: %v, %v", result, err)
	}
}

func TestPanicReleasesInflightClaim(t *testing.T) {
	r := activeRegistry(t)
	broken := newTool("broken")
	broken.run = func(context.Context, string) (*tool.Result, error) { panic("private data") }
	lease, err := r.Register("broken", broken)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ExecuteTool(t.Context(), "broken", "")
	if result != nil || err == nil || err.Error() != "tool broken failed unexpectedly" {
		t.Fatalf("panic result: %v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := lease.Close(ctx); err != nil {
		t.Fatalf("panic leaked inflight claim: %v", err)
	}
}

func TestConcurrentCallsAndClose(t *testing.T) {
	r := activeRegistry(t)
	if _, err := r.Register("echo", newTool("echo")); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			_, err := r.ExecuteTool(t.Context(), "echo", "value")
			if err != nil && !errors.Is(err, registry.ErrUnavailable) && !errors.Is(err, context.Canceled) {
				t.Errorf("concurrent execution: %v", err)
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			<-start
			if err := r.Close(t.Context()); err != nil {
				t.Errorf("concurrent close: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if _, err := r.ExecuteTool(t.Context(), "echo", ""); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("call after close: %v", err)
	}
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("closed registry still advertises tools")
	}
}

func TestCanceledCloseStopsAdmissionAndRetriesDrain(t *testing.T) {
	r := activeRegistry(t)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	work := newTool("work")
	work.run = func(ctx context.Context, _ string) (*tool.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}
	if _, err := r.Register("work", work); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := r.ExecuteTool(t.Context(), "work", "")
		finished <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait: %v", err)
	}
	<-canceled
	if _, err := r.ExecuteTool(t.Context(), "work", ""); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("canceled Close left admission open: %v", err)
	}
	if _, err := r.Register("later", newTool("later")); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("registration after canceled Close: %v", err)
	}
	unblock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("accepted work result: %v", err)
	}
	if err := r.Close(t.Context()); err != nil {
		t.Fatalf("drain retry: %v", err)
	}
}
