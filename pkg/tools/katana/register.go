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
			reg.Register(New().WithLogger(logger).WithProxy(deps.ScannerProxy), "scanner")
		},
	})
}
