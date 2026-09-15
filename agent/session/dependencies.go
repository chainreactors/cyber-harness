package session

import (
 "context"
 "fmt"
 "errors"
 "github.com/chainreactors/cyber/agent"
 "github.com/chainreactors/cyber/agent/provider"
 "github.com/chainreactors/cyber/aop"
 cfg "github.com/chainreactors/cyber/core/config"
 "github.com/chainreactors/cyber/core/events"
 "github.com/chainreactors/cyber/core/eventbus"
 "github.com/chainreactors/cyber/core/hooks"
 "github.com/chainreactors/cyber/core/output"
 "github.com/chainreactors/cyber/core/telemetry"
 "github.com/chainreactors/cyber/core/tool"
 "github.com/chainreactors/cyber/pkg/types"
 "github.com/chainreactors/cyber/skills"
)

// CommandCatalog is discovery only; sessions cannot register or close native commands.
type CommandCatalog interface {
 Names() []string
 GroupNames(string) []string
 All() []*types.CommandSpec
 DescriptionPath(string) string
 UsageDocs() string
}

// Environment supplies explicit borrowed capabilities. It contains no host,
// extension, registry ownership, or service lookup.
type Environment struct {
 Tools tool.Executor
 Commands CommandCatalog
 Bash tool.Tool
 Skills *skills.Store
 Hooks *hooks.Registry
 PublishEvent func(*aop.Event)
 ObserveEvents func(events.Observer) *eventbus.Subscription[*aop.Event]
 ProviderState func() (agent.Provider, agent.ProviderConfig)
 ReloadProvider func(context.Context, agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error)
 SetProvider func(agent.Provider, agent.ProviderConfig)
 ResolveProvider func(*cfg.Option) agent.ProviderConfig
 LLMHealth func() provider.Health
 ScannerState func() string
 SetLogger func(telemetry.Logger)
 Logger func() telemetry.Logger
}

// HistoryStore supplies recovery without coupling the manager to a filesystem.
// Event persistence uses the same canonical event stream as live publication.
type HistoryStore interface {
 Load(context.Context, string) (*History, error)
 Validate(string) error
}
type JSONLHistory struct{}
func (JSONLHistory) Load(ctx context.Context, path string) (*History, error) {
 if err := ctx.Err(); err != nil { return nil, err }
 return ReadHistory(path)
}
func (JSONLHistory) Validate(path string) error { return output.ValidateJSONLTarget(path) }

// NewManager is inert. Start and Close are explicit resource operations and do
// not require a Harness or extension.Scope.
func NewManager(config Config) (*Runtime, error) {
 declared, index, err := commandDeclarations(config.Commands)
 if err != nil { return nil, err }
 if config.Option == nil { config.Option = &cfg.Option{} }
 if config.Logger == nil { config.Logger = telemetry.NopLogger() }
 if config.History == nil { config.History = JSONLHistory{} }
 var env Environment
 if config.Application != nil { env = *config.Application }
 if env.Tools == nil { env.Tools = tool.EmptyExecutor() }
 if env.Skills == nil { env.Skills = skills.NewStore(nil) }
 if env.Hooks == nil { env.Hooks = hooks.New() }
 if env.Commands == nil { env.Commands = emptyCommands{} }
 stream := events.New()
 if env.PublishEvent == nil { env.PublishEvent = stream.Publish }
 if env.ObserveEvents == nil { env.ObserveEvents = stream.Observe }
 state := &provider.State{}
 if env.ProviderState == nil { env.ProviderState = state.Current }
 if env.SetProvider == nil { env.SetProvider = state.Set }
 if env.LLMHealth == nil { env.LLMHealth = state.Health }
 if env.ReloadProvider == nil { env.ReloadProvider = func(ctx context.Context, c agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error) { return state.Reload(ctx, c, config.Logger) } }
 if env.ResolveProvider == nil { env.ResolveProvider = func(o *cfg.Option) agent.ProviderConfig { return agent.ProviderConfig{Provider: o.Provider, Model: o.Model, BaseURL: o.BaseURL, APIKey: o.APIKey} } }
 if env.ScannerState == nil { env.ScannerState = func() string { return "unavailable" } }
 if env.SetLogger == nil { env.SetLogger = func(telemetry.Logger) {} }
 if env.Logger == nil { env.Logger = func() telemetry.Logger { return config.Logger } }
 config.Application = &env
 return &Runtime{
 commands: declared, commandIndex: index, history: config.History,
 app: &env, option: config.Option, logger: config.Logger, runtimeConfig: config,
 sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
 closeDone: make(chan struct{}),
 }, nil
}

type emptyCommands struct{}
var ErrUnavailable = errors.New("session manager is unavailable")
func (e *Environment) Publish(event *aop.Event) { e.PublishEvent(event) }
func (emptyCommands) Names() []string { return nil }
func (emptyCommands) GroupNames(string) []string { return nil }
func (emptyCommands) All() []*types.CommandSpec { return nil }
func (emptyCommands) DescriptionPath(string) string { return "" }
func (emptyCommands) UsageDocs() string { return "" }

// RegisterCommand atomically appends a declaration and all aliases. Existing
// commands are immutable; execution never holds the registration lock.
func (rt *Runtime) RegisterCommand(command Command) error {
 if rt == nil { return fmt.Errorf("session manager is required") }
 rt.lifecycle.Lock()
 defer rt.lifecycle.Unlock()
 if rt.closing { return fmt.Errorf("session manager is closing") }
 rt.commandMu.Lock()
 defer rt.commandMu.Unlock()
 extra := append(append([]Command(nil), rt.commands...), command)
 values, index, err := validateCommands(extra)
 if err != nil { return err }
 rt.commands, rt.commandIndex = values, index
 return nil
}
func (rt *Runtime) lookupCommand(name string) (Command, bool) {
 rt.commandMu.RLock()
 defer rt.commandMu.RUnlock()
 c, ok := rt.commandIndex[name]
 return c, ok
}
