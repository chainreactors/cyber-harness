package namespaces

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Registry is the Point for connection namespace bindings. Each connection
// installs a snapshot; contribution handles remain owned by their Extensions.
// A binding opens its handler per connection, so a shared handler and a
// connection-scoped one are the same contribution in one namespace order.
type Registry struct {
	mu     sync.RWMutex
	order  []protoreflect.FullName
	values map[protoreflect.FullName]aop.Binding
}

func New() *Registry {
	return &Registry{values: make(map[protoreflect.FullName]aop.Binding)}
}

func (c *Registry) Load(scope *extension.Scope) error {
	if c == nil || scope == nil {
		return fmt.Errorf("namespace registry is unavailable")
	}
	return extension.Define[aop.Binding](scope, c)
}

func (c *Registry) Add(values ...aop.Binding) (resource.Handle, error) {
	if c == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	names := make([]protoreflect.FullName, 0, len(values))
	pending := make(map[protoreflect.FullName]aop.Binding, len(values))
	for _, value := range values {
		if value.Prototype == nil || value.Open == nil {
			return nil, fmt.Errorf("invalid namespace binding")
		}
		name := value.Prototype.ProtoReflect().Descriptor().FullName()
		if _, exists := pending[name]; exists {
			return nil, fmt.Errorf("duplicate namespace binding %q", name)
		}
		pending[name] = value
		names = append(names, name)
	}
	c.mu.Lock()
	for _, name := range names {
		if c.registered(name) {
			c.mu.Unlock()
			return nil, fmt.Errorf("duplicate namespace binding %q", name)
		}
	}
	for _, name := range names {
		c.values[name] = pending[name]
		c.order = append(c.order, name)
	}
	c.mu.Unlock()
	return c.revoker(names), nil
}

func (c *Registry) registered(name protoreflect.FullName) bool {
	_, exists := c.values[name]
	return exists
}

func (c *Registry) revoker(names []protoreflect.FullName) resource.Handle {
	closed := false
	return resource.HandleFunc(func(context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		for _, name := range names {
			delete(c.values, name)
		}
		kept := c.order[:0]
		for _, name := range c.order {
			if c.registered(name) {
				kept = append(kept, name)
			}
		}
		c.order = kept
		return nil
	})
}

func (c *Registry) Bind(mux *aop.NamespaceMux) error {
	if c == nil || mux == nil {
		return fmt.Errorf("namespace registry and mux are required")
	}
	c.mu.RLock()
	registrars := make([]func(*aop.NamespaceMux) error, 0, len(c.order))
	for _, name := range c.order {
		if value, exists := c.values[name]; exists {
			registrars = append(registrars, value.Register)
		}
	}
	c.mu.RUnlock()
	for _, register := range registrars {
		if err := register(mux); err != nil {
			return err
		}
	}
	return nil
}

var (
	_ extension.Extension         = (*Registry)(nil)
	_ resource.Point[aop.Binding] = (*Registry)(nil)
)
