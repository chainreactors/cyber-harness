package proton

import (
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "proton",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger := deps.GetLogger()
			cmd := New().WithLogger(logger).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus)
			if rs, ok := deps.Resources.(*resources.Set); ok && rs != nil {
				cmd.WithResourceProvider(rs.ProtonConfig)
			}
			cmd.SetWorkDir(deps.WorkDir)
			reg.Register(commands.Command{Name: cmd.Name(), Usage: cmd.Usage(), Run: cmd.Run, SetProxy: cmd.SetProxy, GetProxy: func() string { return cmd.Proxy }}, "proton")
		},
	})
}
