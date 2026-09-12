package toolgroup_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/extensions/toolgroup"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

type echo struct {
	name string
	run  func(context.Context) error
}

func (e echo) Name() string                 { return e.name }
func (e echo) Description() string          { return "test echo" }
func (e echo) Definition() *tool.Definition { return tool.Def(e.name, e.Description(), struct{}{}) }
func (e echo) Execute(ctx context.Context, args string) (*tool.Result, error) {
	if e.run != nil {
		if err := e.run(ctx); err != nil {
			return nil, err
		}
	}
	return tool.TextResult(args), nil
}

func group(t *testing.T, r tool.Registrar, tools ...tool.Tool) *toolgroup.Extension {
	t.Helper()
	g, err := toolgroup.New(r, tools...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func ownedSet(t *testing.T, entries ...extension.Entry) *extension.Set {
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

func TestSameEntryIDAcrossSetsHasIndependentRegistrationOwnership(t *testing.T) {
	r := registry.New()
	root := ownedSet(t, extension.Entry{ID: "registry", Extension: r})
	if err := root.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	tools := []tool.Tool{echo{name: "first"}}
	a := ownedSet(t, extension.Entry{ID: "tools", Extension: group(t, r, tools...)})
	tools[0] = echo{name: "mutated"} // New owns a snapshot of the supplied slice.
	b := ownedSet(t, extension.Entry{ID: "tools", Extension: group(t, r, echo{name: "second"})})
	if len(r.ToolDefinitions()) != 0 {
		t.Fatal("construction published tools")
	}
	init, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := a.Load(init); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, name := range []string{"first", "second"} {
		if _, err := r.ExecuteTool(t.Context(), name, "ok"); err != nil {
			t.Fatalf("initialization cancellation stopped %s: %v", name, err)
		}
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(t.Context(), "first", ""); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("closed group callable: %v", err)
	}
	if _, err := r.ExecuteTool(t.Context(), "second", "ok"); err != nil {
		t.Fatalf("closing sibling group stopped borrowed registry: %v", err)
	}
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRevokesImmediatelyAndRetainsDependenciesUntilDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := registry.New()
		entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		var resourceClosed, unrelatedClosed bool
		resource := extension.Func{CloseFunc: func(context.Context) error { resourceClosed = true; return nil }}
		work := echo{name: "work", run: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		}}
		s := ownedSet(t,
			extension.Entry{ID: "unrelated", Extension: extension.Func{CloseFunc: func(context.Context) error { unrelatedClosed = true; return nil }}},
			extension.Entry{ID: "registry", Extension: r},
			extension.Entry{ID: "resource", Extension: resource},
			extension.Entry{ID: "tools", DependsOn: []string{"registry", "resource"}, Extension: group(t, r, work)},
		)
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := r.ExecuteTool(t.Context(), "work", ""); finished <- err }()
		<-entered
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := s.Close(ctx)
		if !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close=%v", err)
		}
		<-canceled
		if resourceClosed || !unrelatedClosed {
			t.Fatal("wrong dependency retention during drain")
		}
		if len(r.ToolDefinitions()) != 0 {
			t.Fatal("revoked tools still advertised")
		}
		if _, err := r.ExecuteTool(t.Context(), "work", ""); !errors.Is(err, registry.ErrUnavailable) {
			t.Fatalf("accepted after revocation: %v", err)
		}
		unblock()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("invocation=%v", err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !resourceClosed {
			t.Fatal("dependency survived completed drain")
		}
	})
}

func TestDuplicateToolFailureLeavesExistingLeaseIntact(t *testing.T) {
	r := registry.New()
	root := ownedSet(t, extension.Entry{ID: "registry", Extension: r})
	if err := root.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := ownedSet(t, extension.Entry{ID: "tools", Extension: group(t, r, echo{name: "echo"})})
	b := ownedSet(t, extension.Entry{ID: "tools", Extension: group(t, r, echo{name: "echo"})})
	if err := a.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(t.Context()); !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("Load=%v", err)
	}
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExecuteTool(t.Context(), "echo", "retained"); err != nil {
		t.Fatalf("failed group revoked another lease: %v", err)
	}
}
