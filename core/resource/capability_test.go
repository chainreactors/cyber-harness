package resource_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
)

// greeter is a named interface owned by this package, which is what a
// capability key has to be.
type greeter interface{ greet() string }

type service struct{ name string }

func (s *service) greet() string { return "hello from " + s.name }

func provided(t *testing.T, registry *resource.Registry, position int, value greeter) resource.Handle {
	t.Helper()
	handle, err := resource.Provide[greeter](registry.Scope(position), value)
	if err != nil {
		t.Fatalf("provide at %d: %v", position, err)
	}
	return handle
}

func TestCapabilityIsBorrowedByLaterPositions(t *testing.T) {
	registry := resource.New()
	owner := &service{name: "owner"}
	provided(t, registry, 1, owner)

	value, release, err := resource.Use[greeter](registry.Scope(2))
	if err != nil {
		t.Fatal(err)
	}
	if value.greet() != owner.greet() {
		t.Errorf("borrowed %q, want %q", value.greet(), owner.greet())
	}
	if err := release.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Releasing twice is a no-op, like every other handle here.
	if err := release.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Each case is one rule of the capability contract.
func TestCapabilityWiringFaults(t *testing.T) {
	for _, test := range []struct {
		name   string
		run    func(t *testing.T, registry *resource.Registry) error
		want   error
		detail string
	}{
		{
			name: "provided twice",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 1, &service{name: "first"})
				_, err := resource.Provide[greeter](registry.Scope(2), &service{name: "second"})
				return err
			},
			want:   resource.ErrTypeDefined,
			detail: "capability",
		},
		{
			name: "provided as a capability, then defined as a point",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 1, &service{name: "owner"})
				_, err := resource.Define[greeter](registry.Scope(2), &point[greeter]{})
				return err
			},
			want:   resource.ErrTypeDefined,
			detail: "capability",
		},
		{
			name: "defined as a point, then provided as a capability",
			run: func(t *testing.T, registry *resource.Registry) error {
				if _, err := resource.Define[greeter](registry.Scope(1), &point[greeter]{}); err != nil {
					t.Fatal(err)
				}
				_, err := resource.Provide[greeter](registry.Scope(2), &service{name: "owner"})
				return err
			},
			want:   resource.ErrTypeDefined,
			detail: "contribution point",
		},
		{
			name: "used when nothing provides it",
			run: func(t *testing.T, registry *resource.Registry) error {
				_, _, err := resource.Use[greeter](registry.Scope(1))
				return err
			},
			want:   resource.ErrTypeUnknown,
			detail: "greeter",
		},
		{
			name: "used before its provider is ordered",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 5, &service{name: "late"})
				_, _, err := resource.Use[greeter](registry.Scope(2))
				return err
			},
			want: resource.ErrTypeUnknown,
		},
		{
			name: "used by the extension that provides it",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 3, &service{name: "owner"})
				_, _, err := resource.Use[greeter](registry.Scope(3))
				return err
			},
			want:   resource.ErrInvalid,
			detail: "provided by this extension",
		},
		{
			name: "used after the registry is frozen",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 1, &service{name: "owner"})
				registry.Freeze()
				_, _, err := resource.Use[greeter](registry.Scope(2))
				return err
			},
			want:   resource.ErrDefinitionsFrozen,
			detail: "while loading",
		},
		{
			name: "provided after the registry is frozen",
			run: func(t *testing.T, registry *resource.Registry) error {
				registry.Freeze()
				_, err := resource.Provide[greeter](registry.Scope(1), &service{name: "owner"})
				return err
			},
			want: resource.ErrDefinitionsFrozen,
		},
		{
			name: "added to as though it were a point",
			run: func(t *testing.T, registry *resource.Registry) error {
				provided(t, registry, 1, &service{name: "owner"})
				_, err := resource.Add[greeter](registry.Scope(2), &service{name: "contribution"})
				return err
			},
			want:   resource.ErrInvalid,
			detail: "is a capability, not a contribution point",
		},
		{
			name: "used as though it were a capability",
			run: func(t *testing.T, registry *resource.Registry) error {
				if _, err := resource.Define[greeter](registry.Scope(1), &point[greeter]{}); err != nil {
					t.Fatal(err)
				}
				_, _, err := resource.Use[greeter](registry.Scope(2))
				return err
			},
			want:   resource.ErrInvalid,
			detail: "is a contribution point, not a capability",
		},
		{
			name: "provided a nil value",
			run: func(t *testing.T, registry *resource.Registry) error {
				var missing *service
				_, err := resource.Provide[greeter](registry.Scope(1), missing)
				return err
			},
			want: resource.ErrInvalid,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run(t, resource.New())
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			if test.detail != "" && !strings.Contains(err.Error(), test.detail) {
				t.Errorf("err = %q, want it to mention %q", err, test.detail)
			}
		})
	}
}

// A provider cannot disappear while anyone still holds its value. This is the
// invariant that replaces "get the slice order right and hope".
func TestProviderCannotCloseWhileBorrowed(t *testing.T) {
	registry := resource.New()
	provision := provided(t, registry, 1, &service{name: "owner"})

	_, release, err := resource.Use[greeter](registry.Scope(2))
	if err != nil {
		t.Fatal(err)
	}

	err = provision.Close(context.Background())
	if !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("close with a live borrow = %v, want ErrCloseIncomplete", err)
	}

	// A provider that is closing is no longer available to new borrowers.
	if _, _, err := resource.Use[greeter](registry.Scope(3)); !errors.Is(err, resource.ErrTypeUnknown) {
		t.Errorf("use while closing = %v, want ErrTypeUnknown", err)
	}

	if err := release.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provision.Close(context.Background()); err != nil {
		t.Fatalf("close after the borrow was released: %v", err)
	}
}

// The two directions are keyed differently on purpose: a point by the type of
// the value it stores, a capability by the type describing behaviour. The same
// object can be both, and that is the normal case for a registry.
func TestOneObjectCanBeBothPointAndCapability(t *testing.T) {
	registry := resource.New()
	store := &registryLike{}

	if _, err := resource.Define[string](registry.Scope(1), store); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Provide[greeter](registry.Scope(1), store); err != nil {
		t.Fatal(err)
	}

	if _, err := resource.Add[string](registry.Scope(2), "contributed"); err != nil {
		t.Fatal(err)
	}
	value, _, err := resource.Use[greeter](registry.Scope(2))
	if err != nil {
		t.Fatal(err)
	}
	if value.greet() != "hello from registry" {
		t.Errorf("greet = %q", value.greet())
	}
	if len(store.values) != 1 || store.values[0] != "contributed" {
		t.Errorf("values = %v, want the contributed string", store.values)
	}
}

type registryLike struct{ values []string }

func (r *registryLike) greet() string { return "hello from registry" }

func (r *registryLike) Add(values ...string) (resource.Handle, error) {
	start := len(r.values)
	r.values = append(r.values, values...)
	return handleFunc(func(context.Context) error {
		r.values = r.values[:start]
		return nil
	}), nil
}
