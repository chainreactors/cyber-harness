package toolset_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	"github.com/chainreactors/aiscan/core/tool"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

type registryTool struct {
	def *tool.Definition
	run func(context.Context, string) (*tool.Result, error)
}

func echoTool(name string) *registryTool {
	return &registryTool{def: tool.Def(name, "echo", struct{}{})}
}
func (t *registryTool) Name() string                 { return t.def.Name }
func (t *registryTool) Description() string          { return t.def.Description }
func (t *registryTool) Definition() *tool.Definition { return t.def }
func (t *registryTool) Execute(ctx context.Context, args string) (*tool.Result, error) {
	if t.run != nil {
		return t.run(ctx, args)
	}
	return tool.TextResult(args), nil
}

type contributor struct {
	registry *toolset.Registry
	values   []tool.Tool
	load     func(*extension.Scope) error
	close    func(context.Context) error
}

func (e *contributor) Load(scope *extension.Scope) error {
	if e.load != nil {
		return e.load(scope)
	}
	return e.registry.Register("test", e.values...)
}
func (e *contributor) Close(ctx context.Context) error {
	if e.close != nil {
		return e.close(ctx)
	}
	return nil
}

func registrySet(t *testing.T, entries ...extension.Entry) (*toolset.Registry, *extension.Set) {
	t.Helper()
	registry := toolset.NewRegistry(nil)
	dependencies := make([]string, 0, len(entries))
	for _, entry := range entries {
		dependencies = append(dependencies, entry.ID)
		if value, ok := entry.Extension.(*contributor); ok {
			value.registry = registry
		}
	}
	entries = append(entries, extension.Entry{ID: "registry", DependsOn: dependencies, Extension: registry})
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return registry, set
}

func TestRegistryPublishesOnlyAfterLoad(t *testing.T) {
	first := echoTool("echo")
	registry, set := registrySet(t, extension.Entry{ID: "tools", Extension: &contributor{values: []tool.Tool{first}}})
	if len(registry.ToolDefinitions()) != 0 {
		t.Fatal("staged definitions were published")
	}
	if _, err := registry.ExecuteTool(t.Context(), "echo", ""); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.def.Description = "mutated"
	definitions := registry.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Description != "echo" {
		t.Fatalf("definitions: %v", definitions)
	}
	definitions[0].Name = "mutated"
	if registry.ToolDefinitions()[0].Name != "echo" {
		t.Fatal("caller mutated registry snapshot")
	}
	result, err := registry.ExecuteTool(t.Context(), "echo", "ok")
	if err != nil || tool.ResultText(result) != "ok" {
		t.Fatalf("execution: result=%v err=%v", result, err)
	}
}

func TestRegistryUsesSharedHookBoundary(t *testing.T) {
	r := hooks.New()
	registry := toolset.NewRegistry(r)
	runs, decisions, completions := 0, 0, 0
	value := echoTool("echo")
	value.run = func(_ context.Context, args string) (*tool.Result, error) { runs++; return tool.TextResult(args), nil }
	before := toolhooks.Before.On(r, "policy", func(_ context.Context, ev toolhooks.CallEvent) (toolhooks.Admission, error) {
		decisions++
		if string(ev.Call.GetArguments().GetData()) == "deny" {
			return toolhooks.Admission{Deny: errors.New("denied by test")}, nil
		}
		return toolhooks.Admission{}, nil
	})
	defer before.Cancel()
	completed := toolhooks.Completed.On(r, "observe", func(_ context.Context, ev toolhooks.Completion) (struct{}, error) {
		completions++
		if ev.Result.GetCallId() != ev.Call.GetId() {
			t.Error("correlation changed")
		}
		return struct{}{}, nil
	})
	defer completed.Cancel()
	set, err := extension.New(
		extension.Entry{ID: "tools", Extension: &contributor{registry: registry, values: []tool.Tool{value}}},
		extension.Entry{ID: "registry", DependsOn: []string{"tools"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ExecuteTool(t.Context(), "echo", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ExecuteTool(t.Context(), "echo", "deny"); !errors.Is(err, operation.ErrDenied) {
		t.Fatalf("denied: %v", err)
	}
	if runs != 1 || decisions != 2 || completions != 2 {
		t.Fatalf("runs=%d before=%d completed=%d", runs, decisions, completions)
	}
}

func TestRegistryRegistrationIsAtomic(t *testing.T) {
	registry := toolset.NewRegistry(nil)
	first := &contributor{registry: registry, values: []tool.Tool{echoTool("echo")}}
	second := &contributor{registry: registry, values: []tool.Tool{echoTool("new"), echoTool("echo")}}
	set, err := extension.New(
		extension.Entry{ID: "first", Extension: first},
		extension.Entry{ID: "second", Extension: second},
		extension.Entry{ID: "registry", DependsOn: []string{"first", "second"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); !errors.Is(err, toolset.ErrDuplicate) {
		t.Fatalf("collision: %v", err)
	}
	if len(registry.ToolDefinitions()) != 0 {
		t.Fatal("failed composition published tools")
	}
}

func TestRegistryRejectsInvalidBatchWithoutPartialState(t *testing.T) {
	registry := toolset.NewRegistry(nil)
	value := &contributor{registry: registry, load: func(scope *extension.Scope) error {
		var nilTool *registryTool
		if err := registry.Register("test", echoTool("partial"), nilTool); err == nil {
			t.Fatal("accepted typed nil")
		}
		if err := registry.Register("test", echoTool("duplicate"), echoTool("duplicate")); !errors.Is(err, toolset.ErrDuplicate) {
			t.Fatal(err)
		}
		return registry.Register("test", echoTool("partial"))
	}}
	set, err := extension.New(
		extension.Entry{ID: "tools", Extension: value},
		extension.Entry{ID: "registry", DependsOn: []string{"tools"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if definitions := registry.ToolDefinitions(); len(definitions) != 1 || definitions[0].Name != "partial" {
		t.Fatalf("definitions: %v", definitions)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryDrainProtectsContributorResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		closed := false
		value := echoTool("blocked")
		value.run = func(ctx context.Context, _ string) (*tool.Result, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
			if closed {
				t.Error("resource closed during accepted call")
			}
			return nil, ctx.Err()
		}
		registry, set := registrySet(t, extension.Entry{ID: "resource", Extension: &contributor{
			values: []tool.Tool{value}, close: func(context.Context) error { closed = true; return nil },
		}})
		if err := set.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		callDone := make(chan error, 1)
		go func() {
			_, err := registry.ExecuteTool(t.Context(), "blocked", "")
			callDone <- err
		}()
		<-started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := set.Close(ctx)
		<-canceled
		if !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close timeout: %v", err)
		}
		if closed {
			t.Fatal("timeout released contributor")
		}
		close(release)
		if err := <-callDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := set.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !closed {
			t.Fatal("retry did not release contributor")
		}
	})
}

func TestRegistryPanicReleasesAdmission(t *testing.T) {
	value := echoTool("panic")
	value.run = func(context.Context, string) (*tool.Result, error) { panic("private data") }
	registry, set := registrySet(t, extension.Entry{ID: "tools", Extension: &contributor{values: []tool.Tool{value}}})
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ExecuteTool(t.Context(), "panic", ""); err == nil || err.Error() != "tool panic failed unexpectedly" || !errors.Is(err, operation.ErrPanicked) {
		t.Fatalf("panic: %v", err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			if err := set.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wait.Wait()
}
