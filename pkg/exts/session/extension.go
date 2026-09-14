// Package session owns conversation sessions, runs, inboxes and session commands.
package session

import (
	"context"
	"errors"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
)

var ErrUnavailable = errors.New("session extension is not active")

// Extension owns one session Runtime. Injected application, loop and IOA
// capabilities are dependencies; this extension never closes their owners.
type Extension struct{ runtime *Runtime }

// New is inert. Session hosting always requires an application and its process
// options. Loop is optional so history and control protocols can run without
// selecting a reasoning algorithm.
func New(config Config) (*Extension, error) {
	if config.Application == nil || config.Option == nil {
		return nil, errors.New("session extension requires application and options")
	}
	declared, index, err := commandDeclarations(config.Commands)
	if err != nil {
		return nil, err
	}
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	runtime := &Runtime{
		commands: declared, commandIndex: index,
		app: config.Application, ioa: config.IOA, option: config.Option,
		logger: config.Logger, runtimeConfig: config,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
		closeDone: make(chan struct{}),
	}
	return &Extension{runtime: runtime}, nil
}

func (e *Extension) Runtime() *Runtime {
	if e == nil {
		return nil
	}
	return e.runtime
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.runtime == nil || scope == nil {
		return ErrUnavailable
	}
	return e.runtime.load(scope)
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	return e.runtime.close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
