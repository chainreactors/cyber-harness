package session

import (
	"context"
	"errors"
	"fmt"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/output"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

// HistoryStore supplies recovery without coupling the runtime to a filesystem.
// Event persistence uses the same canonical event stream as live publication.
type HistoryStore interface {
	Load(context.Context, string) (*History, error)
	Validate(string) error
}
type JSONLHistory struct{}

func (JSONLHistory) Load(ctx context.Context, path string) (*History, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ReadHistory(path)
}
func (JSONLHistory) Validate(path string) error { return output.ValidateJSONLTarget(path) }

// Resource owns one Runtime installation. Consumers borrow Runtime while the
// owning extension alone starts and closes Resource.
type Resource struct {
	runtime *Runtime
}

func (r *Resource) Runtime() *Runtime {
	if r == nil {
		return nil
	}
	return r.runtime
}

// NewResource is inert. Start and Close are explicit owner operations and do
// not require an extension.Scope.
func NewResource(config Config) (*Resource, error) {
	declared, index, err := commandDeclarations(config.Commands)
	if err != nil {
		return nil, err
	}
	if config.Option == nil {
		config.Option = &cfg.Option{}
	}
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	if config.History == nil {
		config.History = JSONLHistory{}
	}
	application := config.State
	if application == nil {
		application = &apppkg.State{}
	}
	tools := config.Tools
	if tools == nil {
		tools = tool.EmptyExecutor()
	}
	return &Resource{runtime: &Runtime{
		commands: declared, commandIndex: index, history: config.History,
		app: application, option: config.Option, logger: config.Logger, config: config,
		hooks: config.Hooks, tools: tools, commandRegistry: config.CommandRegistry,
		skills: config.Skills, bash: config.Bash,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
		closeDone: make(chan struct{}),
	}}, nil
}

var ErrUnavailable = errors.New("session runtime is unavailable")

// RegisterCommand atomically appends a declaration and all aliases. Existing
// commands are immutable; execution never holds the registration lock.
func (rt *Runtime) RegisterCommand(command Command) error {
	if rt == nil {
		return fmt.Errorf("session runtime is required")
	}
	rt.lifecycle.Lock()
	defer rt.lifecycle.Unlock()
	if rt.closing {
		return fmt.Errorf("session runtime is closing")
	}
	rt.commandMu.Lock()
	defer rt.commandMu.Unlock()
	extra := append(append([]Command(nil), rt.commands...), command)
	values, index, err := validateCommands(extra)
	if err != nil {
		return err
	}
	rt.commands, rt.commandIndex = values, index
	return nil
}
func (rt *Runtime) lookupCommand(name string) (Command, bool) {
	rt.commandMu.RLock()
	defer rt.commandMu.RUnlock()
	c, ok := rt.commandIndex[name]
	return c, ok
}
