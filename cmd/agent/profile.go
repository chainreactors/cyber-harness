package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	base "github.com/chainreactors/cyber/pkg/base"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
)

type agentProfile struct {
	extensions *extension.Set
	app        *apppkg.App
	runtime    *agentsession.Runtime
	tui        *tuiext.Extension
}

func newAgentProfile(option cfg.Option, logger telemetry.Logger, workDir string, bashTimeout int) (*agentProfile, error) {
	sessionConfig := agentsession.Config{
		NodeName: cfg.ResolveNodeName(option.NodeName),
		Option:   &option, Logger: logger, PrimarySessionID: "main", Loop: agent.StandardLoop{},
	}
	loop := loopext.New(sessionConfig.Loop)
	sessionConfig.Loop = loop.Loop()

	// This build routes nothing, so it publishes the disabled endpoint and
	// links no proxy at all.
	values, err := base.New(base.Config{
		Directory:    workDir,
		SkillPaths:   agentSkillPaths(option.Skills),
		SkillExclude: []string{"cyber"},
		Terminal:     terminalext.Config{Timeout: bashTimeout},
		Provider: provider.StartupConfig{
			Mode: provider.StartupRequired, Config: apppkg.ProviderConfig(&option),
			Fallbacks: apppkg.FallbackProviderConfigs(&option),
		},
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}

	p := &agentProfile{tui: tuiext.New()}
	values = append(values,
		loop,
		sessionext.New(sessionConfig),
		p.tui,
		sessionconsole.New(),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			application, err := extension.Use[*apppkg.App](scope)
			if err != nil {
				return err
			}
			p.app = application
			p.runtime, err = extension.Use[*agentsession.Runtime](scope)
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

func agentSkillPaths(values []string) []string {
	var paths []string
	for _, value := range values {
		if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
			paths = append(paths, value)
		}
	}
	return paths
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
