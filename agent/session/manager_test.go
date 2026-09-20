package session

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
)

func TestStandaloneResourceAndLiveCommandRegistration(t *testing.T) {
	resource, err := newUnitResource(t, nil, Config{Loop: agent.StandardLoop{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Start(t.Context(), t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resource.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	runtime := resource.Runtime()
	first, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.OpenSession(t.Context(), SessionOptions{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	spec := &types.CommandSpec{Name: "/identity", Aliases: []string{"/id"}}
	err = runtime.RegisterCommand(Command{Spec: spec, AdvertiseRemote: true,
		Handler: func(_ context.Context, s *Session, _ []string) (*types.CommandResult, error) {
			// Registration is reentrant; the dispatch lock must not cover handlers.
			_ = runtime.CommandSpecs(true)
			return &types.CommandResult{Content: []*aop.Content{aop.Text(s.ID())}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Name = "/mutated"
	for _, s := range []*Session{first, second} {
		result, err := s.Command(t.Context(), "/id")
		if err != nil || result.Content[0].GetText().Text != s.ID() {
			t.Fatalf("command: %v %v", result, err)
		}
	}
	before := len(runtime.CommandSpecs(false))
	if err := runtime.RegisterCommand(Command{Spec: &types.CommandSpec{Name: "/new", Aliases: []string{"/id"}}, Handler: func(context.Context, *Session, []string) (*types.CommandResult, error) { return nil, nil }}); err == nil {
		t.Fatal("accepted conflicting alias")
	}
	if len(runtime.CommandSpecs(false)) != before {
		t.Fatal("partial command publication")
	}
	if _, ok := runtime.lookupCommand("/new"); ok {
		t.Fatal("conflicting batch leaked")
	}
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterCommand(Command{Spec: &types.CommandSpec{Name: "/late"}, Handler: func(context.Context, *Session, []string) (*types.CommandResult, error) { return nil, nil }}); err == nil {
		t.Fatal("registration after close")
	}
	if _, err := first.Command(t.Context(), "/id"); err == nil {
		t.Fatal("command after close")
	}
}

func TestConcurrentCommandRegistrationAndDiscovery(t *testing.T) {
	resource, err := newUnitResource(t, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Start(t.Context(), t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	runtime := resource.Runtime()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("/custom%d", i)
			if err := runtime.RegisterCommand(Command{Spec: &types.CommandSpec{Name: name}, Handler: func(context.Context, *Session, []string) (*types.CommandResult, error) { return nil, nil }}); err != nil {
				t.Error(err)
			}
			_ = runtime.CommandSpecs(false)
			if _, ok := runtime.lookupCommand(name); !ok {
				t.Errorf("missing %s", name)
			}
		}(i)
	}
	wg.Wait()
}
