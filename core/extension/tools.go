package extension

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/tool"
	"google.golang.org/protobuf/proto"
)

var (
	ErrToolsUnavailable = errors.New("extension tools are not active")
	ErrDuplicateTool    = errors.New("duplicate tool")
	ErrUnknownTool      = errors.New("unknown tool")
)

type installedTool struct {
	value      tool.Tool
	definition *tool.Definition
}

// toolCatalog belongs to a fixed Set. Registration is staged until the entire
// graph loads; shutdown closes admission and drains calls before resources.
type toolCatalog struct {
	mu               sync.Mutex
	entries          map[string]installedTool
	order            []string
	active, stopping bool
	inflight         int
	lifetime         context.Context
	cancel           context.CancelFunc
	done             chan struct{}
}

func newToolCatalog() *toolCatalog {
	ctx, cancel := context.WithCancel(context.Background())
	return &toolCatalog{entries: make(map[string]installedTool), lifetime: ctx, cancel: cancel, done: make(chan struct{})}
}

func (r *toolCatalog) register(tools ...tool.Tool) error {
	pending := make(map[string]installedTool, len(tools))
	var names []string
	for _, t := range tools {
		if t == nil || isNilTool(t) {
			return errors.New("nil tool")
		}
		name, def := t.Name(), t.Definition()
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || def == nil || def.Name != name {
			return errors.New("tool requires a name and matching definition")
		}
		if _, ok := pending[name]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateTool, name)
		}
		pending[name] = installedTool{value: t, definition: proto.Clone(def).(*tool.Definition)}
		names = append(names, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active || r.stopping {
		return ErrToolsUnavailable
	}
	for _, name := range names {
		if _, ok := r.entries[name]; ok {
			return fmt.Errorf("%w: %s", ErrDuplicateTool, name)
		}
	}
	for _, name := range names {
		r.entries[name] = pending[name]
	}
	r.order = append(r.order, names...)
	return nil
}

func (r *toolCatalog) activate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stopping {
		r.active = true
	}
}

func (r *toolCatalog) ToolDefinitions() []*tool.Definition {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return nil
	}
	defs := make([]*tool.Definition, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, proto.Clone(r.entries[name].definition).(*tool.Definition))
	}
	return defs
}

func (r *toolCatalog) ExecuteTool(ctx context.Context, name, arguments string) (result *tool.Result, err error) {
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if !r.active {
		r.mu.Unlock()
		return nil, ErrToolsUnavailable
	}
	t, ok := r.entries[name]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	r.inflight++
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.lifetime, cancel)
	r.mu.Unlock()
	defer func() {
		stop()
		cancel()
		r.mu.Lock()
		r.inflight--
		if r.stopping && r.inflight == 0 {
			close(r.done)
		}
		r.mu.Unlock()
		if recover() != nil {
			result = nil
			err = fmt.Errorf("tool %s failed unexpectedly", name)
		}
	}()
	if r.lifetime.Err() != nil {
		return nil, r.lifetime.Err()
	}
	return t.value.Execute(call, arguments)
}

func (r *toolCatalog) stop() {
	r.mu.Lock()
	if !r.stopping {
		r.stopping = true
		r.active = false
		if r.inflight == 0 {
			close(r.done)
		}
	}
	r.mu.Unlock()
	r.cancel()
}

func (r *toolCatalog) drain(ctx context.Context) error {
	select {
	case <-r.done:
		return nil
	default:
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return errors.Join(ErrCloseIncomplete, ctx.Err())
	}
}

// Executor returns a borrowed view. It exposes nothing before successful Load,
// rejects calls during shutdown, and never grants installation ownership.
func (s *Set) Executor() tool.Executor { return s.tools }

func isNilTool(t tool.Tool) bool {
	v := reflect.ValueOf(t)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
