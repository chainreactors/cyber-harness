package extension_test

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/core/extension"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestScopeTracksEffectsDuringExtensionLifetime(t *testing.T) {
	var order []int
	ext := extension.Func{LoadFunc: func(c *extension.Scope) error {
		err := c.Track(func() { order = append(order, 1) })
		if err != nil {
			return err
		}
		err = c.Track(func() { order = append(order, 2) })
		return err
	}}
	s, err := extension.New(extension.Entry{ID: "tracked", Extension: ext})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(order); got != 2 || order[0] != 2 || order[1] != 1 {
		t.Fatalf("order=%v", order)
	}
}

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

func TestRevocationPrecedesCancellationAndDrain(t *testing.T) {
	var scope *extension.Scope
	var order []string
	s := newSet(t, extension.Entry{ID: "effects", Extension: extension.Func{
		LoadFunc: func(c *extension.Scope) error {
			scope = c
			err := c.Track(func() {
				if c.Lifetime().Err() != nil {
					t.Error("lifetime canceled before revocation")
				}
				if err := c.Track(func() {}); err == nil {
					t.Error("accepted registration while stopping")
				}
				order = append(order, "revoke")
			})
			return err
		},
		CloseFunc: func(context.Context) error {
			if !errors.Is(scope.Lifetime().Err(), context.Canceled) {
				t.Error("Close did not receive lifetime cancellation")
			}
			order = append(order, "drain")
			return nil
		},
	}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := scope.Track(func() {}); err == nil {
		t.Fatal("accepted registration after Close")
	}
	if !reflect.DeepEqual(order, []string{"revoke", "drain"}) {
		t.Fatalf("order=%v", order)
	}
}

func TestRepeatedSetCloseInvokesEffectOnce(t *testing.T) {
	var calls atomic.Int32
	s := newSet(t, extension.Entry{ID: "effect", Extension: extension.Func{
		LoadFunc: func(c *extension.Scope) error {
			return c.Track(func() { calls.Add(1) })
		},
	}})
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("disposals=%d", calls.Load())
	}
}

func TestInitializationCanceledByLastExtensionRollsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var scope *extension.Scope
	var revoked, closed bool
	s := newSet(t, extension.Entry{ID: "last", Extension: extension.Func{
		LoadFunc: func(c *extension.Scope) error {
			scope = c
			if err := c.Track(func() { revoked = true }); err != nil {
				return err
			}
			cancel()
			return nil
		},
		CloseFunc: func(context.Context) error { closed = true; return nil },
	}})
	if err := s.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load=%v", err)
	}
	if !revoked || !closed || scope.Lifetime().Err() == nil {
		t.Fatal("last extension was not rolled back")
	}
	if err := s.Load(t.Context()); err == nil {
		t.Fatal("failed Set was not sealed")
	}
}

func TestRevocationPanicRetainsDependenciesButDrainsOtherWork(t *testing.T) {
	var dependencyClosed, independentClosed, drained, revoked bool
	var drains int
	var lifetime context.Context
	s := newSet(t,
		extension.Entry{ID: "independent", Extension: extension.Func{CloseFunc: func(context.Context) error { independentClosed = true; return nil }}},
		extension.Entry{ID: "dependency", Extension: extension.Func{CloseFunc: func(context.Context) error { dependencyClosed = true; return nil }}},
		extension.Entry{ID: "consumer", DependsOn: []string{"dependency"}, Extension: extension.Func{
			LoadFunc: func(c *extension.Scope) error {
				lifetime = c.Lifetime()
				if err := c.Track(func() { revoked = true }); err != nil {
					return err
				}
				return c.Track(func() { panic("broken revocation") })
			},
			CloseFunc: func(context.Context) error { drains++; drained = true; return nil },
		}},
	)
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Close(t.Context()); !errors.Is(err, extension.ErrCloseIncomplete) {
			t.Fatalf("Close=%v", err)
		}
		if dependencyClosed || !independentClosed || !drained || !revoked || lifetime.Err() == nil {
			t.Fatal("unsafe or interrupted cleanup after revocation panic")
		}
	}
	if drains != 1 {
		t.Fatalf("completed Close repeated %d times", drains)
	}
}
