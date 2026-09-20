package server

import (
	"github.com/chainreactors/cyber/core/resource"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// Declare contributes the server resources needed before argument parsing.
func Declare(resources *resource.Registry, command hostcli.Contribution) error {
	section := Section()
	if _, err := resource.Add[hostcli.Contribution](resources, command); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Section](resources, section)
	return err
}
