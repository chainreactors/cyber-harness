package arsenal

import (
	crtm "github.com/chainreactors/crtm/pkg"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func NewCommand(mgr *crtm.Manager) coretool.Command {
	cmd := &command{mgr: mgr}
	return coretool.Command{
		Name: cmd.Name(), Usage: cmd.Usage(),
		DescriptionPath: "cyber://skills/runtime/arsenal.md",
		Run:             cmd.Run,
	}
}
