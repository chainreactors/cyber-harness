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
// A binding is either a shared handler or an opener that creates one handler
// per connection; both forms share one namespace order.
type Registry struct {
	mu          sync.RWMutex
	order       []protoreflect.FullName
	values      map[protoreflect.FullName]aop.NamespaceBinding
	connections map[protoreflect.FullName]aop.ConnectionBinding
}

func New() *Registry {
	return &Registry{
		values:      make(map[protoreflect.FullName]aop.NamespaceBinding),
		connections: make(map[protoreflect.FullName]aop.ConnectionBinding),
	}
}

func (c *Registry) Load(scope *extension.Scope) error {
	if c == nil || scope == nil {
		return fmt.Errorf("namespace registry is unavailable")
	}
	if err := extension.Define[aop.NamespaceBinding](scope, c); err != nil {
		return err
	}
	return extension.Define[aop.ConnectionBinding](scope, c.ConnectionPoint())
}

func (c *Registry) Add(values ...aop.NamespaceBinding) (resource.Handle, error) {
	if c == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	names := make([]protoreflect.FullName, 0, len(values))
	pending := make(map[protoreflect.FullName]aop.NamespaceBinding, len(values))
	for _, value := range values {
		if value.Prototype == nil || value.Handler == nil {
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

// ConnectionPoint exposes ConnectionBinding through the same resource
// mechanism used by NamespaceBinding. The small adapter is required because Go
// cannot overload Add for both Point[NamespaceBinding] and
// Point[ConnectionBinding] on Registry itself.
func (c *Registry) ConnectionPoint() resource.Point[aop.ConnectionBinding] {
	return connectionPoint{registry: c}
}

type connectionPoint struct{ registry *Registry }

func (p connectionPoint) Add(values ...aop.ConnectionBinding) (resource.Handle, error) {
	c := p.registry
	if c == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	names := make([]protoreflect.FullName, 0, len(values))
	pending := make(map[protoreflect.FullName]aop.ConnectionBinding, len(values))
	for _, value := range values {
		if value.Prototype == nil || value.Open == nil {
			return nil, fmt.Errorf("invalid connection binding")
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
		c.connections[name] = pending[name]
		c.order = append(c.order, name)
	}
	c.mu.Unlock()
	return c.revoker(names), nil
}

func (c *Registry) registered(name protoreflect.FullName) bool {
	if _, exists := c.values[name]; exists {
		return true
	}
	_, exists := c.connections[name]
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
			delete(c.connections, name)
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
			continue
		}
		if value, exists := c.connections[name]; exists {
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
	_ extension.Extension                   = (*Registry)(nil)
	_ resource.Point[aop.NamespaceBinding]  = (*Registry)(nil)
	_ resource.Point[aop.ConnectionBinding] = connectionPoint{}
)
