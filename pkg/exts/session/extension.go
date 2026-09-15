// Package session installs the harness-independent agent/session manager.
package session

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	impl "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

type Config struct {
	BaseSkills       []string
	Commands         []Command
	Application      *apppkg.App
	Environment      *impl.Environment
	History          impl.HistoryStore
	NodeName         string
	Preamble         string
	Option           *cfg.Option
	Logger           telemetry.Logger
	PrimarySessionID string
	PromptConfig     *PromptConfig
	MaxPending       int
	Loop             agent.Loop
}

// Runtime publishes session operations. It never owns the borrowed loop.
type Runtime struct {
	Operations
	application *apppkg.App
}
type Extension struct {
	runtime *Runtime
	manager *impl.Runtime
}

func (e *Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "session", Description: "session service", Provides: []extension.Service{extension.ServiceOf[*Runtime]("session.service")}}
}
func New(config Config) (*Extension, error) {
	env := config.Environment
	if config.Application != nil {
		a := config.Application
		env = &impl.Environment{
			Tools: a.Tools, Commands: a.Commands, Skills: a.Skills, Hooks: a.Hooks,
			PublishEvent: a.Publish, ObserveEvents: a.ObserveEvents,
			ProviderState: a.ProviderState, SetProvider: a.SetProvider,
			ReloadProvider: a.ReloadProvider, ResolveProvider: apppkg.ProviderConfig,
			LLMHealth: a.LLMHealth, ScannerState: a.ScannerState,
			Logger: a.Logger, SetLogger: a.SetLogger,
		}
		if a.Bash != nil {
			env.Bash = a.Bash
		}
	}
	manager, err := impl.NewManager(impl.Config{
		Application: env, History: config.History, BaseSkills: config.BaseSkills,
		Commands: config.Commands, NodeName: config.NodeName, Preamble: config.Preamble,
		Option: config.Option, Logger: config.Logger, PrimarySessionID: config.PrimarySessionID,
		PromptConfig: config.PromptConfig, MaxPending: config.MaxPending, Loop: config.Loop,
	})
	if err != nil {
		return nil, err
	}
	return &Extension{runtime: &Runtime{Operations: manager, application: config.Application}, manager: manager}, nil
}
func (e *Extension) Runtime() *Runtime {
	if e == nil {
		return nil
	}
	return e.runtime
}
func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.runtime == nil || scope == nil {
		return fmt.Errorf("session extension is unavailable")
	}
	return e.manager.Start(scope.Init(), scope.Lifetime())
}
func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.runtime == nil {
		return nil
	}
	return e.manager.Close(ctx)
}
func (rt *Runtime) App() *apppkg.App { return rt.application }

type Session = impl.Session
type SessionOptions = impl.SessionOptions
type SessionCloseReason = impl.SessionCloseReason
type Run = impl.Run
type RunInput = impl.RunInput
type Command = impl.Command
type History = impl.History
type PromptConfig = prompt.PromptConfig
type LoadedSkill = prompt.LoadedSkill

var ReadHistory = impl.ReadHistory
var BuildSystemPrompt = prompt.BuildSystemPrompt
var SystemPromptFunc = prompt.SystemPromptFunc
var ErrUnavailable = impl.ErrUnavailable

const (
	DefaultSessionPendingLimit      = impl.DefaultSessionPendingLimit
	CommandPresentationPlain        = impl.CommandPresentationPlain
	CommandPresentationPreformatted = impl.CommandPresentationPreformatted
	SessionCloseCompleted           = impl.SessionCloseCompleted
	SessionCloseCanceled            = impl.SessionCloseCanceled
	SessionCloseError               = impl.SessionCloseError
	SessionCloseCleared             = impl.SessionCloseCleared
	SessionCloseCompacted           = impl.SessionCloseCompacted
	SessionCloseResumed             = impl.SessionCloseResumed
	SessionCloseRuntime             = impl.SessionCloseRuntime
)

var _ extension.Extension = (*Extension)(nil)
