//go:build full

package katana

import (
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "scanner",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger := deps.GetLogger()
			impl := New().WithLogger(logger).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run, SetProxy: impl.SetProxy, GetProxy: func() string { return impl.Proxy }}, "scanner")
		},
	})
}
