package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	coreregistry "github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

// Registry is the command resource Point and execution boundary for one Profile.
type Registry struct {
	hooks *hooks.Registry
	store *coreregistry.Store[Command]
}

func NewRegistry() *Registry {
	return &Registry{store: coreregistry.New[Command]()}
}

func (r *Registry) Add(commands ...Command) (resource.Handle, error) {
	if r == nil || r.store == nil || len(commands) == 0 {
		return nil, ErrInvalidCommand
	}
	values := make([]coreregistry.Value[Command], 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" || name != command.Name || strings.ContainsAny(name, " \t\r\n") || command.Run == nil {
			return nil, ErrInvalidCommand
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("%w: %s repeated in batch", ErrDuplicateCommand, name)
		}
		seen[name] = struct{}{}
		values = append(values, coreregistry.Value[Command]{Name: name, Value: command})
	}
	return r.store.Add(values...)
}

func (r *Registry) Load(scope *extension.Scope) error {
	if r == nil || r.store == nil || scope == nil {
		return ErrUnavailable
	}
	// The hook registry is a capability, so it is borrowed here rather than
	// handed in at construction: a nil registry silences every admission and
	// observation point below without failing anything.
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	r.hooks = registry
	// A registry makes two statements about itself: it owns the point that
	// stores Commands, and it offers the Executor behaviour. They are separate
	// type keys, so neither shadows the other.
	if err := extension.Define[Command](scope, r); err != nil {
		return err
	}
	if err := extension.Provide[Executor](scope, r); err != nil {
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

func (r *Registry) Get(name string) (*types.CommandSpec, bool) {
	if r == nil || r.store == nil {
		return nil, false
	}
	entry, exists := r.store.Get(name)
	if !exists {
		return nil, false
	}
	return commandSpec(entry.Value), true
}

func (r *Registry) Has(name string) bool {
	_, exists := r.Get(name)
	return exists
}

func (r *Registry) All() []*types.CommandSpec {
	if r == nil || r.store == nil {
		return nil
	}
	entries := r.store.Entries()
	result := make([]*types.CommandSpec, 0, len(entries))
	for _, entry := range entries {
		result = append(result, commandSpec(entry.Value))
	}
	return result
}

func (r *Registry) Names() []string {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Names()
}

func (r *Registry) DescriptionPath(name string) string {
	if r == nil || r.store == nil {
		return ""
	}
	entry, exists := r.store.Get(name)
	if !exists {
		return ""
	}
	return entry.Value.DescriptionPath
}

func (r *Registry) Execute(ctx context.Context, name string, execution *Execution) (result any, err error) {
	if r == nil || r.store == nil || execution == nil {
		return nil, fmt.Errorf("command %s requires an execution", name)
	}
	entry, call, release, err := r.store.Acquire(ctx, name)
	if err != nil {
		if errors.Is(err, coreregistry.ErrUnknown) {
			return nil, fmt.Errorf("unknown command: %s", name)
		}
		return nil, err
	}
	defer release()
	if err := call.Err(); err != nil {
		return nil, err
	}
	resourceID, err := execution.waitID(call)
	if err != nil {
		return nil, err
	}
	call, finish := operation.Begin(call, "command", name)
	defer finish(nil)
	call = operation.ContextWithResource(call, resourceID)
	correlation := operation.Correlation(call)
	event := toolhooks.CommandEvent{
		Operation: proto.Clone(correlation).(*operationpb.Ref), Name: name,
		Args: append([]string(nil), execution.Args...), Directory: execution.Dir,
	}
	var startedAt time.Time
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(context.WithoutCancel(call), "command panicked", "command", name, "stack", string(debug.Stack()))
			result = nil
			err = operation.PanicError("command", name)
		}
		if cause := context.Cause(call); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
		endedAt := time.Now()
		if toolhooks.CommandCompleted.Has(r.hooks) {
			hooks.Notify(context.WithoutCancel(call), r.hooks, toolhooks.CommandCompleted, toolhooks.CommandCompletion{
				Lifecycle: toolhooks.Lifecycle{
					Operation: proto.Clone(correlation).(*operationpb.Ref), StartedAt: startedAt, EndedAt: endedAt, Err: err,
				},
				Command: event,
			})
		}
	}()
	if toolhooks.BeforeCommand.Has(r.hooks) {
		admission, hookErr := toolhooks.BeforeCommand.Emit(call, r.hooks, event)
		if err = toolhooks.Check(admission, hookErr); err != nil {
			return nil, err
		}
	}
	if err = context.Cause(call); err != nil {
		return nil, err
	}
	startedAt = time.Now()
	if toolhooks.CommandStarted.Has(r.hooks) {
		hooks.Notify(call, r.hooks, toolhooks.CommandStarted, event)
	}
	if err = context.Cause(call); err != nil {
		return nil, err
	}
	return entry.Value.Run(call, execution)
}

func (r *Registry) Run(ctx context.Context, tokens []string, parent *Execution) (any, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	args, err := StripShellSyntax(tokens[1:])
	if err != nil {
		return nil, err
	}
	name := tokens[0]
	args = NormalizeNoColor(name, args)
	if parent == nil {
		return nil, fmt.Errorf("command %s requires an execution", name)
	}
	parent.mu.RLock()
	child := &Execution{
		ID: parent.ID, Command: name, Args: args, Dir: parent.Dir, Env: parent.Env,
		Stdin: parent.Stdin, Stdout: parent.Stdout, Stderr: parent.Stderr,
		manager: parent.manager,
	}
	parent.mu.RUnlock()
	return r.Execute(ctx, name, child)
}

func (r *Registry) UsageDocs() string {
	var text strings.Builder
	for _, command := range r.All() {
		if command.Description != "" {
			text.WriteString(command.Description)
			text.WriteByte('\n')
			continue
		}
		first := command.Usage
		if index := strings.IndexByte(first, '\n'); index > 0 {
			first = first[:index]
		}
		first = strings.TrimSpace(first)
		if !strings.HasPrefix(first, command.Name) {
			first = command.Name
		}
		text.WriteString("- ")
		text.WriteString(first)
		text.WriteByte('\n')
	}
	return text.String()
}

func commandSpec(command Command) *types.CommandSpec {
	return &types.CommandSpec{Name: command.Name, Usage: command.Usage, Description: command.QuickReference}
}

var _ extension.Extension = (*Registry)(nil)
var _ resource.Point[Command] = (*Registry)(nil)
