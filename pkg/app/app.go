package app

import (
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type App struct {
	Providers provider.State
	Commands  commands.Executor
	Tools     tool.Executor
	Bash      *terminaltool.BashTool
	Hooks     *hooks.Registry
	Skills    *skills.Store
	events    *coreevents.Stream
	Progress  *eventbus.Bus[*toolpb.Progress]
	loggerMu  sync.RWMutex
	logger    telemetry.Logger
}

// Dependencies are concrete values selected by the composition root. App
// borrows them; their Extensions retain lifecycle ownership.
type Dependencies struct {
	Skills   *skills.Store
	Hooks    *hooks.Registry
	Events   *coreevents.Stream
	Commands commands.Executor
	Tools    tool.Executor
	Bash     *terminaltool.BashTool
}

// New constructs an inert application around extensions selected by its profile.
func New(logger telemetry.Logger, dependencies Dependencies) (*App, error) {
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
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	a := &App{
		logger: logger,
		Hooks:  dependencies.Hooks, events: dependencies.Events,
		Progress: eventbus.New[*toolpb.Progress](), Commands: dependencies.Commands,
		Tools: dependencies.Tools, Bash: dependencies.Bash, Skills: dependencies.Skills,
	}
	return a, nil
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
