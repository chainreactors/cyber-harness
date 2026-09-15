package toolset

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coreregistry "github.com/chainreactors/cyber/core/registry"
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

// Registry is the single Agent-tool publication and execution boundary for one
// product composition. Tools are registered during extension loading and are
// immutable after activation. Close rejects new work, cancels accepted calls,
// and drains them before tool owners and their resources close.
type Registry struct {
	hooks *hooks.Registry
	store *coreregistry.Store[registeredTool]
}

func NewRegistry(hooks *hooks.Registry) *Registry {
	return &Registry{hooks: hooks, store: coreregistry.New[registeredTool]()}
}

// Register atomically adds fixed declarations before activation. The registry
// retains them for the whole composition; tool resources remain borrowed.
func (r *Registry) Register(source string, tools ...tool.Tool) error {
	if r == nil || r.store == nil || len(tools) == 0 {
		return coreregistry.ErrInvalid
	}
	values := make([]coreregistry.Value[registeredTool], 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, value := range tools {
		if isNilTool(value) {
			return errors.New("nil tool")
		}
		name, definition := value.Name(), value.Definition()
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || definition == nil || definition.Name != name {
			return errors.New("tool requires a name and matching definition")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: %s (source %s repeated in batch)", ErrDuplicate, name, source)
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
	_, err := r.store.Register(source, "", values...)
	return err
}

func (r *Registry) Load(scope *extension.Scope) error {
	if r == nil || r.store == nil || scope == nil {
		return ErrUnavailable
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
var _ extension.Extension = (*Registry)(nil)
