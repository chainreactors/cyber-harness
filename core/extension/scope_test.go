package extension_test

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/core/extension"
	"testing"
)

func TestScopeLifetimeIsIndependentOfInitialization(t *testing.T) {
	init, cancel := context.WithCancel(t.Context())
	defer cancel()
	var scopes []*extension.Scope
	makeSet := func() *extension.Set {
		return newSet(t, extension.Entry{ID: "same", Extension: extension.Func{
			LoadFunc: func(c *extension.Scope) error { scopes = append(scopes, c); return nil },
			CloseFunc: func(context.Context) error {
				return nil
			},
		}})
	}
	a, b := makeSet(), makeSet()
	if err := a.Load(init); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, c := range scopes {
		if !errors.Is(c.Init().Err(), context.Canceled) || c.Lifetime().Err() != nil {
			t.Fatal("initialization cancellation stopped lifetime")
		}
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(scopes[0].Lifetime().Err(), context.Canceled) || scopes[1].Lifetime().Err() != nil {
		t.Fatal("scope lifetimes are not isolated")
	}
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestInitializationCanceledByLastExtensionRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var scope *extension.Scope
	var closed bool
	s := newSet(t, extension.Entry{ID: "last", Extension: extension.Func{
		LoadFunc: func(c *extension.Scope) error {
			scope = c
			cancel()
			return nil
		},
		CloseFunc: func(context.Context) error { closed = true; return nil },
	}})
	if err := s.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load=%v", err)
	}
	if !closed || scope.Lifetime().Err() == nil {
		t.Fatal("last extension was not rolled back")
	}
	if err := s.Load(t.Context()); err == nil {
		t.Fatal("failed Set was not sealed")
	}
}

func TestIncompleteClosePreservesDependencyLifetime(t *testing.T) {
	var dependency, consumer *extension.Scope
	var attempts, dependencyCloses int
	s := newSet(t,
		extension.Entry{ID: "dependency", Extension: extension.Func{
			LoadFunc: func(s *extension.Scope) error { dependency = s; return nil },
			CloseFunc: func(context.Context) error {
				dependencyCloses++
				if dependency.Lifetime().Err() == nil {
					t.Error("dependency Close started without lifetime cancellation")
				}
				return nil
			},
		}},
		extension.Entry{ID: "consumer", DependsOn: []string{"dependency"}, Extension: extension.Func{
			LoadFunc: func(s *extension.Scope) error { consumer = s; return nil },
			CloseFunc: func(context.Context) error {
				attempts++
				if consumer.Lifetime().Err() == nil || dependency.Lifetime().Err() != nil {
					t.Error("consumer must be canceled while its dependency remains live")
				}
				if attempts == 1 {
					return context.DeadlineExceeded
				}
				return nil
			},
		}},
	)
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("first Close = %v", err)
	}
	if dependencyCloses != 0 {
		t.Fatal("incomplete consumer released dependency")
	}
	for range 2 {
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 2 || dependencyCloses != 1 {
		t.Fatalf("consumer closes=%d dependency closes=%d", attempts, dependencyCloses)
	}
}
