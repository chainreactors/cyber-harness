// Package session installs the harness-independent session runtime.
package session

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type Extension struct {
	config   session.Config
	resource *session.Resource
}

// New selects session parameters. Load borrows capabilities from their owners.
func New(config session.Config) *Extension { return &Extension{config: config} }

// Runtime is the session runtime. It is nil until this extension has loaded.
func (e *Extension) Runtime() *session.Runtime {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Runtime()
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("session extension is unavailable")
	}
	providers, err := extension.Use[*provider.State](scope)
	if err != nil {
		return err
	}
	hookRegistry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	tools, err := extension.Use[coretool.Executor](scope)
	if err != nil {
		return err
	}
	commandRegistry, err := extension.Use[coretool.CommandExecutor](scope)
	if err != nil {
		return err
	}
	store, err := extension.Use[*skills.Store](scope)
	if err != nil {
		return err
	}
	bash, err := extension.Use[*terminaltool.BashTool](scope)
	if err != nil {
		return err
	}
	loop, err := extension.Use[agent.Loop](scope)
	if err != nil {
		return err
	}
	promptResolver, err := extension.Use[prompt.Resolver](scope)
	if err != nil {
		return err
	}
	config := e.config
	config.Loop = loop
	config.Providers = providers
	if config.Events, err = extension.Use[*events.Stream](scope); err != nil {
		return err
	}
	if config.Logger, err = extension.Use[telemetry.Logger](scope); err != nil {
		return err
	}
	if config.History == nil {
		config.History = session.JSONLHistory{}
	}
	config.Hooks, config.Tools, config.CommandRegistry = hookRegistry, tools, commandRegistry
	config.Skills, config.Shell = store, bash
	config.PromptResolver = promptResolver
	resource, err := session.NewResource(config)
	if err != nil {
		return err
	}
	e.resource = resource
	if err := extension.Provide[*session.Runtime](scope, resource.Runtime()); err != nil {
		return err
	}

	return e.resource.Start(scope.Init(), scope.Lifetime())
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
