package results

import "github.com/chainreactors/aiscan/pkg/command"

func init() {
	command.RegisterFactory(command.Factory{
		Group: "results",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			reg.Register(&ParseResultsCommand{}, "results")
			reg.Register(&FilterResultsCommand{}, "results")
		},
	})
}
