// Package registry provides an explicitly owned tool executor. It has no Agent,
// model, transport, command-shell, or product dependencies.
package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"google.golang.org/protobuf/proto"
)

var (
	ErrUnavailable = errors.New("tool registry or owner is not active")
	ErrDuplicate   = errors.New("tool or owner already registered")
	ErrUnknown     = errors.New("unknown tool or owner")
)

type lifecycle uint8

const (
	newState lifecycle = iota
	activeState
	stoppingState
	closedState
)

type owner struct {
	ctx      context.Context
	cancel   context.CancelFunc
	stopping bool
	inflight int
	done     chan struct{}
}

type entry struct {
	tool       tool.Tool
	definition *tool.Definition
	owner      *owner
}

// Registry implements tool.Executor and extension.Extension. Construct it with New.
// It owns registrations and invocation cancellation, but never closes tools or
// their borrowed resources. Tool extensions unregister and wait before releasing
// their own resources. Names and owner IDs cannot be reused in this instance.
type Registry struct {
	mu       sync.Mutex
	state    lifecycle
	owners   map[string]*owner
	entries  map[string]entry
	order    []string
	inflight int
	done     chan struct{}
}

var _ tool.Executor = (*Registry)(nil)
var _ tool.Registrar = (*Registry)(nil)
var _ extension.Extension = (*Registry)(nil)

func New() *Registry {
	return &Registry{
		owners:  make(map[string]*owner),
		entries: make(map[string]entry),
		done:    make(chan struct{}),
	}
}

// Load enables registration and execution. ctx bounds this operation only;
// canceling it after Load returns does not stop the registry.
func (r *Registry) Load(scope *extension.Context) error {
	ctx := scope.Init()
	return r.loadContext(ctx)
}

// LoadContext activates a registry assembled outside a Set. New code should
// place Registry in an extension.Set; this helper keeps legacy App assembly
// explicit while the product profile is migrated.
func (r *Registry) LoadContext(ctx context.Context) error { return r.loadContext(ctx) }

func (r *Registry) loadContext(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.state == stoppingState || r.state == closedState {
		return ErrUnavailable
	}
	r.state = activeState
	return nil
}

// Register publishes a complete owner's tool group atomically. A failed group
// publishes nothing. The caller must pass valid, non-nil tool instances and
// stop mutating them after publication. Definitions are cloned on registration.
func (r *Registry) Register(id string, tools ...tool.Tool) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || len(tools) == 0 {
		return fmt.Errorf("registration requires an owner and at least one tool")
	}
	pending := make(map[string]entry, len(tools))
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			return fmt.Errorf("owner %s: nil tool", id)
		}
		name, def := t.Name(), t.Definition()
		if strings.TrimSpace(name) == "" || def == nil || def.Name != name {
			return fmt.Errorf("owner %s: tool must have a name and matching definition", id)
		}
		if _, exists := pending[name]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicate, name)
		}
		pending[name] = entry{tool: t, definition: proto.Clone(def).(*tool.Definition)}
		names = append(names, name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != activeState {
		return ErrUnavailable
	}
	if _, exists := r.owners[id]; exists {
		return fmt.Errorf("%w: owner %s", ErrDuplicate, id)
	}
	for _, name := range names {
		if _, exists := r.entries[name]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicate, name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &owner{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	r.owners[id] = o
	for _, name := range names {
		e := pending[name]
		e.owner = o
		r.entries[name] = e
	}
	r.order = append(r.order, names...)
	return nil
}

// ToolDefinitions returns independent snapshots, in registration order. Stopped
// owners disappear immediately, even while their accepted calls are draining.
func (r *Registry) ToolDefinitions() []*tool.Definition {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != activeState {
		return nil
	}
	defs := make([]*tool.Definition, 0, len(r.order))
	for _, name := range r.order {
		e := r.entries[name]
		if !e.owner.stopping {
			defs = append(defs, proto.Clone(e.definition).(*tool.Definition))
		}
	}
	return defs
}

func (r *Registry) ExecuteTool(ctx context.Context, name, arguments string) (result *tool.Result, err error) {
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if r.state != activeState {
		r.mu.Unlock()
		return nil, ErrUnavailable
	}
	e, ok := r.entries[name]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if e.owner.stopping {
		r.mu.Unlock()
		return nil, ErrUnavailable
	}
	// Admission and accounting share the same lock as owner shutdown.
	e.owner.inflight++
	r.inflight++
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.owner.ctx, cancel)
	r.mu.Unlock()

	defer func() {
		stop()
		cancel()
		r.mu.Lock()
		e.owner.inflight--
		r.inflight--
		if e.owner.stopping && e.owner.inflight == 0 {
			close(e.owner.done)
		}
		r.finishClose()
		r.mu.Unlock()
		if recover() != nil {
			result = nil
			err = fmt.Errorf("tool %s failed unexpectedly", name)
		}
	}()
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	return e.tool.Execute(callCtx, arguments)
}

// UnregisterOwner stops discovery and admission, cancels accepted calls, and
// waits for them. A deadline does not release the owner's resources or permit
// ID reuse. Retry with a fresh context. Unknown IDs cannot affect other owners.
func (r *Registry) UnregisterOwner(ctx context.Context, id string) error {
	r.mu.Lock()
	o, ok := r.owners[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	stopOwner(o)
	r.mu.Unlock()
	o.cancel()
	return wait(ctx, o.done)
}

// Close stops all owners and waits for accepted invocations. It initiates
// shutdown even if ctx is already canceled, so canceled startup rollback still
// stops admission. It never starts a goroutine to hide an uncooperative tool.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.state != closedState {
		r.state = stoppingState
	}
	cancels := make([]context.CancelFunc, 0, len(r.owners))
	for _, o := range r.owners {
		stopOwner(o)
		cancels = append(cancels, o.cancel)
	}
	r.finishClose()
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if err := wait(ctx, r.done); err != nil {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return nil
}

// These helpers are called with r.mu held.
func stopOwner(o *owner) {
	if !o.stopping {
		o.stopping = true
		if o.inflight == 0 {
			close(o.done)
		}
	}
}

func (r *Registry) finishClose() {
	if r.state == stoppingState && r.inflight == 0 {
		r.state = closedState
		close(r.done)
	}
}

func wait(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
