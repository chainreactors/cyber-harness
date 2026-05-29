package websearch

import "github.com/chainreactors/aiscan/pkg/command"

func init() {
	command.RegisterFactory(command.Factory{
		Group: "tools",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			ws := New(deps.TavilyKeys)
			if deps.ScannerProxy != "" {
				ws.SetProxy(deps.ScannerProxy)
			}
			reg.Register(ws, "tools")
		},
	})
}
