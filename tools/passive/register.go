package passive

import (
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func NewCommand(backend Engine, logger telemetry.Logger) coretool.Command {
	impl := New(backend).WithLogger(logger)
	return coretool.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "cyber://skills/passive/passive.md",
		Run:             impl.Run,
	}
}
