package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
)

// clock is a named interface owned by this package: what a capability key has
// to be.
type clock interface{ now() int }

type stoppedClock struct {
	value    int
	torndown bool
}

func (c *stoppedClock) now() int { return c.value }

type provider struct {
	value  *stoppedClock
	closed bool
}

func (p *provider) Load(scope *extension.Scope) error {
	return extension.Provide[clock](scope, p.value)
}

func (p *provider) Close(context.Context) error {
	p.closed = true
	p.value.torndown = true
	return nil
}

type consumer struct {
	borrowed clock
	// duringClose records what the consumer saw while shutting down.
	sawTeardown bool
	loadErr     error
}

func (c *consumer) Load(scope *extension.Scope) error {
	value, err := extension.Use[clock](scope)
	c.loadErr = err
	if err != nil {
		return err
	}
	c.borrowed = value
	return nil
}

func (c *consumer) Close(context.Context) error {
	// An extension drains work that still calls a borrowed value here, so the
	// value must still be standing.
	if value, ok := c.borrowed.(*stoppedClock); ok && value.torndown {
		c.sawTeardown = true
	}
	return nil
}

func TestCapabilityCrossesExtensions(t *testing.T) {
	owner := &provider{value: &stoppedClock{value: 7}}
	borrower := &consumer{}
	set, err := extension.New(owner, borrower)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if borrower.borrowed == nil || borrower.borrowed.now() != 7 {
		t.Fatalf("borrowed %v, want the provided clock", borrower.borrowed)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if borrower.sawTeardown {
		t.Error("the provider was torn down while its borrower was still closing")
	}
	if !owner.closed {
		t.Error("provider never closed")
	}
}

// This is the failure the whole mechanism exists to convert: a misordered slice
// used to yield a nil value discovered at runtime. Now it names the type and
// the extension at load.
func TestCapabilityOrderingFailureNamesTheType(t *testing.T) {
	borrower := &consumer{}
	owner := &provider{value: &stoppedClock{value: 1}}
	set, err := extension.New(borrower, owner) // deliberately backwards
	if err != nil {
		t.Fatal(err)
	}
	err = set.Load(context.Background())
	if err == nil {
		t.Fatal("expected the load to fail")
	}
	if !errors.Is(err, resource.ErrTypeUnknown) {
		t.Fatalf("err = %v, want ErrTypeUnknown", err)
	}
	for _, want := range []string{"clock", "extension 0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if borrower.borrowed != nil {
		t.Error("the borrower kept a value from a failed load")
	}
}

type selfUser struct{ err error }

func (s *selfUser) Load(scope *extension.Scope) error {
	if err := extension.Provide[clock](scope, &stoppedClock{value: 3}); err != nil {
		return err
	}
	_, s.err = extension.Use[clock](scope)
	return nil
}

func (s *selfUser) Close(context.Context) error { return nil }

// Self-use would strand teardown: the provision closes before this extension's
// Close, the borrow releases after, so the reference count would never reach
// zero.
func TestExtensionCannotBorrowWhatItProvides(t *testing.T) {
	value := &selfUser{}
	set, err := extension.New(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(value.err, resource.ErrInvalid) {
		t.Fatalf("self-use err = %v, want ErrInvalid", value.err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatalf("close after a rejected self-use: %v", err)
	}
}

type lateUser struct {
	scope *extension.Scope
	err   error
}

func (l *lateUser) Load(scope *extension.Scope) error {
	l.scope = scope
	return nil
}

func (l *lateUser) Close(context.Context) error { return nil }

// Use belongs in a Load body. Keeping a Scope and reaching for a capability
// later is how load order stops meaning anything.
func TestCapabilityCannotBeBorrowedAfterLoad(t *testing.T) {
	owner := &provider{value: &stoppedClock{value: 5}}
	late := &lateUser{}
	set, err := extension.New(owner, late)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, late.err = extension.Use[clock](late.scope)
	if !errors.Is(late.err, resource.ErrDefinitionsFrozen) {
		t.Fatalf("late use = %v, want ErrDefinitionsFrozen", late.err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type failing struct{}

func (failing) Load(*extension.Scope) error { return errors.New("deliberate") }
func (failing) Close(context.Context) error { return nil }

// A load that fails partway must leave nothing borrowed, or the provider can
// never close.
func TestFailedLoadReleasesBorrows(t *testing.T) {
	owner := &provider{value: &stoppedClock{value: 9}}
	borrower := &consumer{}
	set, err := extension.New(owner, borrower, failing{})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err == nil {
		t.Fatal("expected the load to fail")
	}
	if !owner.closed {
		t.Error("the provider did not close during rollback, so a borrow outlived the failure")
	}
}
