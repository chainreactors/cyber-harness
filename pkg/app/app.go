package app

import (
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/agent/provider"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
)

// State owns what no extension does: the provider state a host reconfigures at
// runtime, the progress bus, the event stream it publishes on, and the logger.
// It borrows nothing. Everything an extension owns is reached as a capability,
// by the extension that needs it, rather than parked here for others to read.
type State struct {
	Providers provider.State
	Progress  *eventbus.Bus[*toolpb.Progress]
	events    *coreevents.Stream
	loggerMu  sync.RWMutex
	logger    telemetry.Logger
}

// New constructs inert shared state around the event stream its host owns.
func New(logger telemetry.Logger, stream *coreevents.Stream) (*State, error) {
	if stream == nil {
		return nil, fmt.Errorf("application requires an event stream")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &State{
		logger: logger, events: stream,
		Progress: eventbus.New[*toolpb.Progress](),
	}, nil
}

// Events is the stream this state publishes on. A host publishes the same
// stream as a capability, so that consumers and the state observe
// one sequence rather than two.
func (a *State) Events() *coreevents.Stream {
	if a == nil {
		return nil
	}
	return a.events
}

func (a *State) Logger() telemetry.Logger {
	return appLogger{app: a}
}

func (a *State) SetLogger(logger telemetry.Logger) {
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

func (a *State) currentLogger() telemetry.Logger {
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
	app *State
}

func (l appLogger) Debugf(format string, args ...any) { l.app.currentLogger().Debugf(format, args...) }
func (l appLogger) Infof(format string, args ...any)  { l.app.currentLogger().Infof(format, args...) }
func (l appLogger) Warnf(format string, args ...any)  { l.app.currentLogger().Warnf(format, args...) }
func (l appLogger) Errorf(format string, args ...any) { l.app.currentLogger().Errorf(format, args...) }
func (l appLogger) Importantf(format string, args ...any) {
	l.app.currentLogger().Importantf(format, args...)
}
