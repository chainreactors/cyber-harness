package cyberhub

import (
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/resources"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "cyberhub",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			res, _ := deps.Resources.(*resources.Set)
			if res == nil {
				return
			}
			reg.Register(New(res), "cyberhub")
		},
	})
}
