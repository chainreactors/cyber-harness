package resource_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
)

type point[T any] struct {
	mu     sync.Mutex
	values []T
}

func (p *point[T]) Add(values ...T) (resource.Handle, error) {
	p.mu.Lock()
	start := len(p.values)
	p.values = append(p.values, values...)
	p.mu.Unlock()
	return handleFunc(func(context.Context) error {
		p.mu.Lock()
		p.values = p.values[:start]
		p.mu.Unlock()
		return nil
	}), nil
}

type handleFunc func(context.Context) error

func (f handleFunc) Close(ctx context.Context) error { return f(ctx) }

func TestTypedDefinitionsAndContributions(t *testing.T) {
	registry := resource.New()
	strings := &point[string]{}
	definition, err := resource.Define(registry.Scope(1), strings)
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := resource.Add(registry.Scope(2), "one", "two")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.values); got != 2 {
		t.Fatalf("values = %d", got)
	}
	if err := definition.Close(context.Background()); !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("definition close = %v", err)
	}
	if err := contribution.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := definition.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEarlierScopeCannotUseLaterDefinition(t *testing.T) {
	registry := resource.New()
	if _, err := resource.Define(registry.Scope(2), &point[int]{}); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Add(registry.Scope(1), 1); !errors.Is(err, resource.ErrTypeUnknown) {
		t.Fatalf("add = %v", err)
	}
}

func TestFreezeOnlyFreezesTypes(t *testing.T) {
	registry := resource.New()
	if _, err := resource.Define(registry, &point[string]{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	if _, err := resource.Define(registry, &point[int]{}); !errors.Is(err, resource.ErrDefinitionsFrozen) {
		t.Fatalf("define = %v", err)
	}
	if _, err := resource.Add(registry, "hot"); err != nil {
		t.Fatalf("hot add = %v", err)
	}
}
