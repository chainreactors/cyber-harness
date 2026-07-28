package tools

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func buildTestGroups(groups []string, deps *commands.Deps, reg *commands.CommandRegistry) {
	commands.BuildPlan(capability.Select(capability.Options{Groups: groups}), deps, reg)
}
