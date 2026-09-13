package arsenal

import (
	"github.com/chainreactors/aiscan/pkg/commands"
)

func NewCommand() (commands.Command, error) {
	cmd, err := NewArsenalCommand()
	if err != nil {
		return commands.Command{}, err
	}
	return commands.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/arsenal.md",
		Run:             cmd.Run,
	}, nil
}
