package session

import (
	"context"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/types"
)

func TestStandaloneResourceSharesImmutableCommands(t *testing.T) {
	spec := &types.CommandSpec{Name: "/identity", Aliases: []string{"/id"}}
	resource, err := newUnitResource(t, nil, Config{Commands: []Command{{
		Spec: spec, AdvertiseRemote: true,
		Handler: func(_ context.Context, s *Session, _ []string) (*types.CommandResult, error) {
			_ = s.state.runtime.CommandSpecs(true)
			return &types.CommandResult{Content: []*aop.Content{aop.Text(s.ID())}}, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// The runtime owns declarations, including aliases, from construction onward.
	spec.Name, spec.Aliases[0] = "/mutated", "/changed"
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
	var wg sync.WaitGroup
	for _, s := range []*Session{first, second} {
		wg.Go(func() {
			for range 12 {
				// Discovery returns owned copies, even while other sessions execute.
				for _, declared := range runtime.CommandSpecs(true) {
					declared.Name = "/external"
					if len(declared.Aliases) > 0 {
						declared.Aliases[0] = "/outside"
					}
				}
				result, err := s.Command(t.Context(), "/id")
				if err != nil {
					t.Error(err)
					return
				}
				if result.GetContent()[0].GetText().GetText() != s.ID() {
					t.Errorf("wrong session: %v", result)
				}
			}
		})
	}
	wg.Wait()
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Command(t.Context(), "/id"); err == nil {
		t.Fatal("command after close")
	}
}
