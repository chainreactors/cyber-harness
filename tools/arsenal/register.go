package arsenal

import (
	coretool "github.com/chainreactors/cyber/core/tool"
)

func NewCommand(directory string) (coretool.Command, error) {
	cmd, err := NewArsenalCommand(directory)
	if err != nil {
		return coretool.Command{}, err
	}
	return coretool.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/runtime/arsenal.md",
		Run:             cmd.Run,
	}, nil
}
