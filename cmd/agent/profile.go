package main

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	harness "github.com/chainreactors/cyber/pkg/harness"
)

type agentProfile struct {
	extensions *extension.Set
	runtime    *agentsession.Runtime
	bindings   *consoleapi.Registry
}

func newAgentProfile(option cfg.Option, logger telemetry.Logger, workDir string, bashTimeout int) (*agentProfile, error) {
	sessionConfig := sessionext.ConfigFromOption(&option, agentsession.Config{
		NodeName:         cfg.ResolveNodeName(option.NodeName),
		PrimarySessionID: "main", Loop: agent.StandardLoop{},
	})
	loop := loopext.New(sessionConfig.Loop)

	// This build routes nothing, so it publishes the disabled endpoint and
	// links no proxy at all.
	values, err := harness.BaseExtensions(harness.BaseConfig{
		Directory:  workDir,
		SkillPaths: cfg.LocalSkillPaths(option.Skills),
		Terminal:   terminalext.Config{Timeout: bashTimeout},
		Provider: provider.StartupConfig{
			Mode: provider.StartupRequired, Config: cfg.ProviderConfig(&option),
			Fallbacks: cfg.FallbackProviderConfigs(&option),
		},
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}

	p := &agentProfile{}
	values = append(values,
		loop,
		subagentext.New(), sessionext.New(sessionConfig), subagentext.NewTools(),
		tuiext.New(),
		sessionext.NewConsole(),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			if p.runtime, err = extension.Use[*agentsession.Runtime](scope); err != nil {
				return err
			}
			p.bindings, err = extension.Use[*consoleapi.Registry](scope)
			return err
		}},
	)
	set, err := extension.New(values...)
	if err != nil {
		return nil, err
	}
	p.extensions = set
	return p, nil
}

func (p *agentProfile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("agent profile is unavailable")
	}
	return p.extensions.Load(ctx)
}

func (p *agentProfile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}

// ConsoleBindings publishes the presentation contributions this profile
// assembled. It is nil until the graph is active.
func (p *agentProfile) ConsoleBindings() *consoleapi.Bindings {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.bindings == nil {
		return nil
	}
	return p.bindings.Bindings()
}

// Runtime is the session runtime this profile assembled.
func (p *agentProfile) Runtime() (*agentsession.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.runtime == nil {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.runtime, nil
}
