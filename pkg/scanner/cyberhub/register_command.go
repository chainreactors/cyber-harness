package cyberhub

import (
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/scanner/resources"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "cyberhub",
		Build: func(deps *command.Deps) []command.PseudoCommand {
			res, _ := deps.Resources.(*resources.Set)
			if res == nil {
				return nil
			}
			return []command.PseudoCommand{New(res)}
		},
	})
}
