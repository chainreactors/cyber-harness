package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func activeRegistry(t *testing.T, values ...Contribution) (*Registry, []interface{ Close(context.Context) error }) {
	t.Helper()
	registry := NewRegistry()
	handles := make([]interface{ Close(context.Context) error }, 0, len(values))
	for _, value := range values {
		handle, err := registry.Add(value)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
	}
	if err := registry.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })
	return registry, handles
}

func add(name string, target Target, id, text string) Contribution {
	return Contribution{
		Name: name, Targets: []Target{target},
		Apply: func(_ context.Context, document *Document, _ Context) error {
			return document.Add(id, Static(text))
		},
	}
}

func TestRegistryUsesRegistrationOrderAndTargetIsolation(t *testing.T) {
	registry, _ := activeRegistry(t,
		add("first", MainSystem, "first", "one"),
		add("other-target", ScannerSystem, "scanner", "scanner-only"),
		add("last", MainSystem, "last", "two"),
	)
	result := registry.Build(t.Context(), Context{Target: MainSystem})
	if result.Prompt != "one\n\ntwo" || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDocumentOperations(t *testing.T) {
	registry, _ := activeRegistry(t,
		Contribution{Name: "base", Targets: []Target{MainSystem}, Apply: func(_ context.Context, document *Document, _ Context) error {
			if err := document.Add("a", Static("a")); err != nil {
				return err
			}
			return document.Add("c", Static("c"))
		}},
		Contribution{Name: "edit", Targets: []Target{MainSystem}, Apply: func(_ context.Context, document *Document, _ Context) error {
			if err := document.Before("c", "b", Static("b")); err != nil {
				return err
			}
			if err := document.After("c", "d", Static("d")); err != nil {
				return err
			}
			if err := document.Replace("a", Static("A")); err != nil {
				return err
			}
			document.Remove("d")
			return nil
		}},
	)
	if got := registry.Build(t.Context(), Context{Target: MainSystem}).Prompt; got != "A\n\nb\n\nc" {
		t.Fatalf("edited prompt = %q", got)
	}

	if _, err := registry.Add(Contribution{Name: "reset", Targets: []Target{MainSystem}, Apply: func(_ context.Context, document *Document, _ Context) error {
		return document.Reset("only", Static("reset"))
	}}); err != nil {
		t.Fatal(err)
	}
	if got := registry.Build(t.Context(), Context{Target: MainSystem}).Prompt; got != "reset" {
		t.Fatalf("reset prompt = %q", got)
	}
}

func TestContributionFailureRollsBackDocument(t *testing.T) {
	registry, _ := activeRegistry(t,
		add("base", MainSystem, "base", "kept"),
		Contribution{Name: "failed", Targets: []Target{MainSystem}, Apply: func(_ context.Context, document *Document, _ Context) error {
			if err := document.Add("leak", Static("must not leak")); err != nil {
				return err
			}
			return errors.New("apply failed")
		}},
		add("after", MainSystem, "after", "after"),
	)
	result := registry.Build(t.Context(), Context{Target: MainSystem})
	if result.Prompt != "kept\n\nafter" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Contribution != "failed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRegistryReportsRendererFailuresAndPanics(t *testing.T) {
	registry, _ := activeRegistry(t,
		Contribution{Name: "errors", Targets: []Target{MainSystem}, Apply: func(_ context.Context, document *Document, _ Context) error {
			if err := document.Add("error", func(context.Context, Context) (string, error) {
				return "", errors.New("render failed")
			}); err != nil {
				return err
			}
			return document.Add("panic", func(context.Context, Context) (string, error) {
				panic("render panic")
			})
		}},
		Contribution{Name: "apply-panic", Targets: []Target{MainSystem}, Apply: func(context.Context, *Document, Context) error {
			panic("apply panic")
		}},
	)
	result := registry.Build(t.Context(), Context{Target: MainSystem})
	if result.Prompt != "" || len(result.Diagnostics) != 3 {
		t.Fatalf("result = %#v", result)
	}
	joined := result.Diagnostics[0].Message + result.Diagnostics[1].Message + result.Diagnostics[2].Message
	for _, want := range []string{"render failed", "render panic", "apply panic"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics missing %q: %#v", want, result.Diagnostics)
		}
	}
}

func TestContributionHandleRevokesAndDrainsRendering(t *testing.T) {
	started := make(chan struct{})
	registry, handles := activeRegistry(t, Contribution{
		Name: "blocking", Targets: []Target{MainSystem},
		Apply: func(_ context.Context, document *Document, _ Context) error {
			return document.Add("blocking", func(ctx context.Context, _ Context) (string, error) {
				close(started)
				<-ctx.Done()
				return "", ctx.Err()
			})
		},
	})
	built := make(chan Result, 1)
	go func() { built <- registry.Build(context.Background(), Context{Target: MainSystem}) }()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- handles[0].Close(context.Background()) }()

	select {
	case result := <-built:
		if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "canceled") {
			t.Fatalf("build result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel the renderer")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not drain the renderer")
	}
	if got := registry.Build(t.Context(), Context{Target: MainSystem}).Prompt; got != "" {
		t.Fatalf("revoked contribution still rendered %q", got)
	}
}
