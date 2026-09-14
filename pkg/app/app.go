package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/agent/provider"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

type App struct {
	Providers provider.State
	config    Config
	Commands  commands.Runtime
	Tools     tool.Executor
	Bash      *commands.BashTool
	Hooks     *hooks.Registry
	Skills    *skills.Store
	events    *coreevents.Stream
	Progress  *eventbus.Bus[*toolpb.Progress]
	scanner   Scanner
	closed    bool
	stateMu   sync.Mutex
	lifecycle sync.Mutex
	loaded    bool
	closing   bool
	loggerMu  sync.RWMutex
	logger    telemetry.Logger
}

// Resource owns App initialization and shutdown. Profiles retain Resource and
// publish App, whose API contains no lifecycle operations.
type Resource struct {
	App *App
}

var _ extension.Extension = (*Resource)(nil)

// AppServices are profile-selected business capabilities. App uses them but
// never loads or closes their owning resources. The profile resolves these
// services from Extension Providers before constructing App.
type AppServices struct {
	Skills   *skills.Store
	Hooks    *hooks.Registry
	Events   *coreevents.Stream
	Commands commands.Runtime
	Tools    tool.Executor
	Bash     *commands.BashTool
	Scanner  Scanner
}

var (
	ToolsService    = extension.ServiceOf[tool.Executor]("tools.executor")
	CommandsService = extension.ServiceOf[commands.Runtime]("commands.runtime")
	SkillsService   = extension.ServiceOf[*skills.Store]("skills.store")
	HooksService    = extension.ServiceOf[*hooks.Registry]("hooks.registry")
	EventsService   = extension.ServiceOf[*coreevents.Stream]("events.stream")
	BashService     = extension.ServiceOf[*commands.BashTool]("commands.bash")
	ScannerService  = extension.ServiceOf[Scanner]("scanner.service")
)

// Services resolves the App consumer contract from a sealed Profile table.
func Services(table *extension.Services) (AppServices, error) {
	tools, err := extension.Get[tool.Executor](table, ToolsService)
	if err != nil {
		return AppServices{}, err
	}
	commandRuntime, err := extension.Get[commands.Runtime](table, CommandsService)
	if err != nil {
		return AppServices{}, err
	}
	skills, err := extension.Get[*skills.Store](table, SkillsService)
	if err != nil {
		return AppServices{}, err
	}
	hooks, err := extension.Get[*hooks.Registry](table, HooksService)
	if err != nil {
		return AppServices{}, err
	}
	events, err := extension.Get[*coreevents.Stream](table, EventsService)
	if err != nil {
		return AppServices{}, err
	}
	result := AppServices{Tools: tools, Commands: commandRuntime, Skills: skills, Hooks: hooks, Events: events}
	if value, present, resolveErr := extension.Optional[Scanner](table, ScannerService); resolveErr != nil {
		return AppServices{}, resolveErr
	} else if present {
		result.Scanner = value
	}
	if value, present, resolveErr := extension.Optional[*commands.BashTool](table, BashService); resolveErr != nil {
		return AppServices{}, resolveErr
	} else if present {
		result.Bash = value
	}
	return result, nil
}

// Scanner is the read-only readiness side of the profile-owned scanner
// extension. App reports it to callers but never starts or closes it.
type Scanner interface {
	Wait(context.Context) error
	State() string
}

// New constructs an inert application around extensions selected by its profile.
func New(rc Config, dependencies AppServices) (*Resource, error) {
	if dependencies.Hooks == nil {
		return nil, fmt.Errorf("application requires profile hooks")
	}
	if dependencies.Events == nil {
		return nil, fmt.Errorf("application requires profile events")
	}
	if dependencies.Commands == nil {
		return nil, fmt.Errorf("application requires profile command registry")
	}
	if dependencies.Tools == nil {
		return nil, fmt.Errorf("application requires profile tool registry")
	}
	if dependencies.Skills == nil {
		dependencies.Skills = skills.NewStore(nil)
	}
	logger := rc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a := &App{
		config: rc, logger: logger,
		Hooks: dependencies.Hooks, events: dependencies.Events,
		Progress: eventbus.New[*toolpb.Progress](), Commands: dependencies.Commands,
		Tools: dependencies.Tools, Bash: dependencies.Bash, scanner: dependencies.Scanner, Skills: dependencies.Skills,
	}
	return &Resource{App: a}, nil
}

// Load initializes application state. Capability resources and registries are
// sibling entries in the owning profile's Set; App never creates a nested Set.
func (r *Resource) Load(scope *extension.Scope) error {
	if r == nil || r.App == nil || scope == nil {
		return fmt.Errorf("application is required")
	}
	a := r.App
	ctx := scope.Init()
	if ctx == nil {
		ctx = context.Background()
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if a.loaded {
		return nil
	}
	if a.closing {
		return fmt.Errorf("application is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.loaded = true
	return nil
}

func (a *App) Logger() telemetry.Logger {
	return appLogger{app: a}
}

func (a *App) SetLogger(logger telemetry.Logger) {
	if a == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if proxy, ok := logger.(appLogger); ok && proxy.app == a {
		return
	}
	a.loggerMu.Lock()
	a.logger = logger
	a.loggerMu.Unlock()
}

func (a *App) currentLogger() telemetry.Logger {
	if a == nil {
		return telemetry.NopLogger()
	}
	a.loggerMu.RLock()
	logger := a.logger
	a.loggerMu.RUnlock()
	if logger == nil {
		return telemetry.NopLogger()
	}
	return logger
}

type appLogger struct {
	app *App
}

func (l appLogger) Debugf(format string, args ...any) { l.app.currentLogger().Debugf(format, args...) }
func (l appLogger) Infof(format string, args ...any)  { l.app.currentLogger().Infof(format, args...) }
func (l appLogger) Warnf(format string, args ...any)  { l.app.currentLogger().Warnf(format, args...) }
func (l appLogger) Errorf(format string, args ...any) { l.app.currentLogger().Errorf(format, args...) }
func (l appLogger) Importantf(format string, args ...any) {
	l.app.currentLogger().Importantf(format, args...)
}

func (a *App) WaitEngines(ctx context.Context) error {
	if a == nil || a.scanner == nil {
		return nil
	}
	return a.scanner.Wait(ctx)
}

func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.App == nil {
		return nil
	}
	a := r.App
	a.lifecycle.Lock()
	a.closing = true
	a.loaded = false
	a.lifecycle.Unlock()
	a.stateMu.Lock()
	a.closed = true
	a.stateMu.Unlock()
	return nil
}

func (a *App) Closed() bool {
	if a == nil {
		return true
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.closed
}
