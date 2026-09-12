package extension_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
)

type catalogTool struct {
	def *tool.Definition
	run func(context.Context, string) (*tool.Result, error)
}

func echoTool(name string) *catalogTool {
	return &catalogTool{def: tool.Def(name, "echo", struct{}{})}
}
func (t *catalogTool) Name() string                 { return t.def.Name }
func (t *catalogTool) Description() string          { return t.def.Description }
func (t *catalogTool) Definition() *tool.Definition { return t.def }
func (t *catalogTool) Execute(ctx context.Context, args string) (*tool.Result, error) {
	if t.run != nil {
		return t.run(ctx, args)
	}
	return tool.TextResult(args), nil
}

type catalogExtension struct {
	load  func(*extension.Context) error
	close func(context.Context) error
}

func (e *catalogExtension) Load(c *extension.Context) error { return e.load(c) }
func (e *catalogExtension) Close(c context.Context) error {
	if e.close != nil {
		return e.close(c)
	}
	return nil
}

func catalogSet(t *testing.T, entries ...extension.Entry) *extension.Set {
	t.Helper()
	s, err := extension.New(entries...)
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

func TestToolsPublishOnlyAfterEntireSetLoads(t *testing.T) {
	var saved *extension.Context
	var s *extension.Set
	first := echoTool("echo")
	s = catalogSet(t,
		extension.Entry{ID: "first", Extension: &catalogExtension{load: func(c *extension.Context) error {
			saved = c
			return c.RegisterTools(first)
		}}},
		extension.Entry{ID: "second", Extension: &catalogExtension{load: func(c *extension.Context) error {
			if len(s.Executor().ToolDefinitions()) != 0 {
				t.Fatal("partial catalog published")
			}
			if _, err := s.Executor().ExecuteTool(c.Init(), "echo", ""); !errors.Is(err, extension.ErrToolsUnavailable) {
				t.Fatal(err)
			}
			if err := saved.RegisterTools(echoTool("late")); !errors.Is(err, extension.ErrToolsUnavailable) {
				t.Fatalf("retained context registered tools: %v", err)
			}
			return nil
		}}},
	)
	executor := s.Executor()
	init, cancel := context.WithCancel(t.Context())
	if err := s.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel()
	first.def.Description = "mutated"
	defs := executor.ToolDefinitions()
	if len(defs) != 1 || defs[0].Description != "echo" {
		t.Fatalf("definitions: %v", defs)
	}
	defs[0].Name = "mutated"
	if executor.ToolDefinitions()[0].Name != "echo" {
		t.Fatal("snapshot mutates catalog")
	}
	if result, err := executor.ExecuteTool(t.Context(), "echo", "ok"); err != nil || tool.ResultText(result) != "ok" {
		t.Fatalf("startup cancellation reached invocation: %v", err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(executor.ToolDefinitions()) != 0 {
		t.Fatal("closed catalog remained visible")
	}
	if _, err := executor.ExecuteTool(t.Context(), "echo", ""); !errors.Is(err, extension.ErrToolsUnavailable) {
		t.Fatal(err)
	}
}

func TestToolConflictRollsBackWholeSet(t *testing.T) {
	var closed []string
	entry := func(id string, tools ...tool.Tool) extension.Entry {
		return extension.Entry{ID: id, Extension: &catalogExtension{
			load:  func(c *extension.Context) error { return c.RegisterTools(tools...) },
			close: func(context.Context) error { closed = append(closed, id); return nil },
		}}
	}
	s := catalogSet(t, entry("first", echoTool("echo")), entry("second", echoTool("new"), echoTool("echo")))
	if err := s.Load(t.Context()); !errors.Is(err, extension.ErrDuplicateTool) {
		t.Fatalf("collision: %v", err)
	}
	if len(closed) != 2 || closed[0] != "second" || closed[1] != "first" {
		t.Fatalf("rollback: %v", closed)
	}
	if len(s.Executor().ToolDefinitions()) != 0 {
		t.Fatal("failed load published tools")
	}
	if err := s.Load(t.Context()); err == nil {
		t.Fatal("failed set reloaded")
	}
}

func TestRejectedToolBatchDoesNotStagePartialDefinitions(t *testing.T) {
	s := catalogSet(t, extension.Entry{ID: "tools", Extension: &catalogExtension{load: func(c *extension.Context) error {
		var nilTool *catalogTool
		if err := c.RegisterTools(echoTool("partial"), nilTool); err == nil {
			t.Fatal("accepted typed nil")
		}
		if err := c.RegisterTools(echoTool("duplicate"), echoTool("duplicate")); !errors.Is(err, extension.ErrDuplicateTool) {
			t.Fatal(err)
		}
		return c.RegisterTools(echoTool("partial"))
	}}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if defs := s.Executor().ToolDefinitions(); len(defs) != 1 || defs[0].Name != "partial" {
		t.Fatalf("partial batch: %v", defs)
	}
}

func TestToolDrainTimeoutRetainsResourcesAndCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		closed := false
		value := echoTool("blocked")
		value.run = func(ctx context.Context, _ string) (*tool.Result, error) {
			if tool.InvocationFromContext(ctx).CallID != "call" {
				t.Error("lost invocation context")
			}
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			if closed {
				t.Error("resource closed during accepted call")
			}
			return nil, ctx.Err()
		}
		s := catalogSet(t, extension.Entry{ID: "resource", Extension: &catalogExtension{
			load:  func(c *extension.Context) error { return c.RegisterTools(value) },
			close: func(context.Context) error { closed = true; return nil },
		}})
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		callDone := make(chan error, 1)
		go func() {
			_, err := s.Executor().ExecuteTool(tool.ContextWithInvocation(t.Context(), tool.Invocation{CallID: "call"}), "blocked", "")
			callDone <- err
		}()
		<-started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := s.Close(ctx)
		<-canceled
		if !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("close timeout: %v", err)
		}
		if closed {
			t.Error("timeout released resource")
		}
		if _, err := s.Executor().ExecuteTool(t.Context(), "blocked", ""); !errors.Is(err, extension.ErrToolsUnavailable) {
			t.Error(err)
		}
		close(release)
		if err := <-callDone; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !closed {
			t.Fatal("retry did not release resource")
		}
	})
}

func TestToolPanicReleasesAdmissionAndConcurrentClose(t *testing.T) {
	value := echoTool("panic")
	value.run = func(context.Context, string) (*tool.Result, error) { panic("private data") }
	s := catalogSet(t, extension.Entry{ID: "tools", Extension: &catalogExtension{load: func(c *extension.Context) error { return c.RegisterTools(value) }}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Executor().ExecuteTool(t.Context(), "panic", ""); err == nil || err.Error() != "tool panic failed unexpectedly" {
		t.Fatalf("panic: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
