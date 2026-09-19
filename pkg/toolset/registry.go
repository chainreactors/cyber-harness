package toolset

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coreregistry "github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"google.golang.org/protobuf/proto"
)

var (
	ErrUnavailable = coreregistry.ErrUnavailable
	ErrDuplicate   = coreregistry.ErrDuplicate
	ErrUnknown     = coreregistry.ErrUnknown
)

type registeredTool struct {
	tool       tool.Tool
	definition *tool.Definition
}

// Registry is the tool resource Point and execution boundary for one Profile.
type Registry struct {
	hooks *hooks.Registry
	store *coreregistry.Store[registeredTool]
}

func NewRegistry() *Registry {
	return &Registry{store: coreregistry.New[registeredTool]()}
}

func (r *Registry) Add(tools ...tool.Tool) (resource.Handle, error) {
	if r == nil || r.store == nil || len(tools) == 0 {
		return nil, coreregistry.ErrInvalid
	}
	values := make([]coreregistry.Value[registeredTool], 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, value := range tools {
		if isNilTool(value) {
			return nil, errors.New("nil tool")
		}
		name, definition := value.Name(), value.Definition()
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || definition == nil || definition.Name != name {
			return nil, errors.New("tool requires a name and matching definition")
		}
		if _, exists := seen[name]; exists {
			return nil, ErrDuplicate
		}
		seen[name] = struct{}{}
		values = append(values, coreregistry.Value[registeredTool]{
			Name: name,
			Value: registeredTool{
				tool:       value,
				definition: proto.Clone(definition).(*tool.Definition),
			},
		})
	}
	return r.store.Add(values...)
}

func (r *Registry) Load(scope *extension.Scope) error {
	if r == nil || r.store == nil || scope == nil {
		return ErrUnavailable
	}
	// The hook registry is a capability, so it is borrowed here rather than
	// handed in at construction: a nil registry silences the whole execution
	// hook chain without failing anything.
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	r.hooks = registry
	// The point stores Tools; the capability offers the Executor behavior.
	if err := extension.Define[tool.Tool](scope, r); err != nil {
		return err
	}
	if err := extension.Provide[tool.Executor](scope, r); err != nil {
		return err
	}
	return r.store.Activate(scope.Init())
}

func (r *Registry) Close(ctx context.Context) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close(ctx)
}

func (r *Registry) ToolDefinitions() []*tool.Definition {
	if r == nil || r.store == nil {
		return nil
	}
	entries := r.store.Entries()
	definitions := make([]*tool.Definition, 0, len(entries))
	for _, entry := range entries {
		definitions = append(definitions, proto.Clone(entry.Value.definition).(*tool.Definition))
	}
	return definitions
}

func (r *Registry) ExecuteTool(ctx context.Context, name, arguments string) (result *tool.Result, err error) {
	if r == nil || r.store == nil {
		return nil, ErrUnavailable
	}
	entry, call, release, err := r.store.Acquire(ctx, name)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := call.Err(); err != nil {
		return nil, err
	}
	return toolhooks.Execute(call, r.hooks, name, arguments, entry.Value.tool.Execute)
}

func isNilTool(value tool.Tool) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ tool.Executor = (*Registry)(nil)
var _ resource.Point[tool.Tool] = (*Registry)(nil)
var _ extension.Extension = (*Registry)(nil)
