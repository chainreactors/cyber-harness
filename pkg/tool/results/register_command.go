package results

import "github.com/chainreactors/aiscan/pkg/command"

func init() {
	command.RegisterFactory(command.Factory{
		Group: "results",
		Build: func(deps *command.Deps) []command.PseudoCommand {
			return []command.PseudoCommand{
				&ParseResultsCommand{},
				&FilterResultsCommand{},
			}
		},
	})
}
