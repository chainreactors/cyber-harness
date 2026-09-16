package server

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
)

// Declare contributes the server resources needed before argument parsing.
func Declare(resources *resource.Registry, command hostcli.Contribution, legacyAlias bool) error {
	section := Section()
	if legacyAlias {
		section.Aliases = []string{"ioa"}
	}
	if _, err := resource.Add[hostcli.Contribution](resources, command); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Section](resources, section)
	return err
}
