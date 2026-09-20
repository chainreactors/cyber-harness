// Package native contributes the built-in Agent tools and commands.
package native

import (
	"fmt"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	looptool "github.com/chainreactors/cyber/tools/loop"
)

type Extension struct{}

func New() *Extension { return &Extension{} }

func (e *Extension) Load(scope *extension.Scope) error {
	store, err := extension.Use[*skills.Store](scope)
	if err != nil {
		return err
	}
	subagent := agent.NewSubAgentTool(func(name string) (agent.AgentType, error) {
		skill, ok := store.ByName(name)
		if !ok {
			return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
		}
		if !skill.Agent {
			return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
		}
		return agent.AgentType{
			FormattedPrompt: store.FormatInvocation(skill, ""),
			Model:           skill.AgentModel, Background: skill.AgentBackground,
		}, nil
	})
	if err := extension.Add[coretool.Tool](scope, subagent); err != nil {
		return err
	}
	return extension.Add(scope, looptool.NewCommand())
}

var _ extension.Extension = (*Extension)(nil)
