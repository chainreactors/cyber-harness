package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	providerext "github.com/chainreactors/cyber/pkg/exts/provider"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/tools/files"
)

type agentProfile struct {
	extensions *extension.Set
	app        *apppkg.App
	runtime    *agentsession.Runtime
	tui        *tuiext.Extension
}

func newAgentProfile(option cfg.Option, logger telemetry.Logger, workDir string, bashTimeout int) (*agentProfile, error) {
	hookRegistry := hooks.New()
	eventStream := events.New()
	commandRegistry := commands.NewRegistry(hookRegistry)
	toolRegistry := toolset.NewRegistry(hookRegistry)
	providerConfig := provider.StartupConfig{
		Mode: provider.StartupRequired, Config: apppkg.ProviderConfig(&option),
		Fallbacks: apppkg.FallbackProviderConfigs(&option),
	}
	library, err := skillsext.NewLibrary(skillsext.LibraryConfig{
		Directory: workDir, Paths: agentSkillPaths(option.Skills), Exclude: []string{"cyber"},
	})
	if err != nil {
		return nil, err
	}
	workspace, err := fileext.New(hookRegistry, files.Config{Directory: workDir})
	if err != nil {
		return nil, err
	}
	terminal, err := terminalext.New(hookRegistry, commandRegistry, terminalext.Config{
		Directory: workDir, Timeout: bashTimeout,
	})
	if err != nil {
		return nil, err
	}
	application, err := apppkg.New(logger, apppkg.Dependencies{
		Hooks: hookRegistry, Events: eventStream, Commands: commandRegistry,
		Tools: toolRegistry, Skills: library.Store(), Bash: terminal.Bash(),
	})
	if err != nil {
		return nil, err
	}
	provider, err := providerext.New(&application.Providers, providerConfig, application.Logger())
	if err != nil {
		return nil, err
	}
	loop := loopext.New(agent.StandardLoop{})
	tui := tuiext.New()
	session, err := sessionext.New(agentsession.Config{
		Application: application, NodeName: cfg.ResolveNodeName(option.NodeName),
		Option: &option, Logger: logger, PrimarySessionID: "main", Loop: loop.Runtime(),
	})
	if err != nil {
		return nil, err
	}
	presentation, err := sessionconsole.New(session.Runtime())
	if err != nil {
		return nil, err
	}
	set, err := extension.New(
		commandRegistry, toolRegistry, library, provider, workspace, terminal,
		loop, tui, session, presentation,
	)
	if err != nil {
		return nil, err
	}
	return &agentProfile{extensions: set, app: application, runtime: session.Runtime(), tui: tui}, nil
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
