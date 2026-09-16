// Package native contributes the built-in Agent tools and commands.
package native

import (
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	looptool "github.com/chainreactors/cyber/tools/loop"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
)

type Extension struct {
	application   *app.App
	proxy         *proxytool.ProxyHub
	fallbackProxy string
}

func New(application *app.App, proxy *proxytool.ProxyHub, fallbackProxy string) (*Extension, error) {
	if application == nil || application.Commands == nil {
		return nil, fmt.Errorf("native resources require an application and commands")
	}
	return &Extension{application: application, proxy: proxy, fallbackProxy: fallbackProxy}, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	subagent := agent.NewSubAgentTool(func(name string) (agent.AgentType, error) {
		skill, ok := e.application.Skills.ByName(name)
		if !ok {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		if !skill.Agent {
			return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
		}
		return agent.AgentType{
			FormattedPrompt: e.application.Skills.FormatInvocation(skill, ""),
			Model:           skill.AgentModel, Background: skill.AgentBackground,
		}, nil
	})
	if err := extension.Add[tool.Tool](scope, subagent); err != nil {
		return err
	}
	loop := looptool.NewCommand()
	commands := []commands.Command{loop}
	if e.proxy != nil {
		proxyCommands := proxytool.NewCommands(e.application.Commands.Run, e.proxy, e.fallbackProxy)
		commands = append(commands, proxyCommands...)
	}
	return extension.Add(scope, commands...)
}

var _ extension.Extension = (*Extension)(nil)
