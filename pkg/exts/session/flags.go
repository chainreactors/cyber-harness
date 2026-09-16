package session

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
)

// FlagGroups preserves the typed option schema and its existing defaults.
// Declaration and --help never require an App or a loaded extension.
func FlagGroups(options *cfg.AgentOptions) []cfg.FlagGroup {
	return []cfg.FlagGroup{{Name: "Agent Options", Options: options}}
}

// Declare contributes Session's inert flags before the runtime Session is
// constructed. The host decides whether the CLI resource type exists.
func Declare(resources *resource.Registry, options *cfg.AgentOptions) error {
	_, err := resource.Add[hostcli.Contribution](resources, func(registry *hostcli.Registry) error {
		return registry.Group("agent", "", cfg.FlagGroup{Name: "Agent Options", Options: options})
	})
	return err
}
