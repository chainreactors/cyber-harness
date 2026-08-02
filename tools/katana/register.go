//go:build full

package katana

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	capability.Register(capability.Descriptor{ID: "katana", Kind: capability.KindScanner, Group: "scanner", CLIName: "katana", Summary: "katana", UsageLine: "  katana         Run katana web crawler", Usage: func() string { return New().Usage() }, Skills: []string{"katana"}})
	commands.RegisterFactory(commands.Factory{
		Capability: "katana",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			logger := deps.GetLogger()
			impl := New().WithLogger(logger).WithProxy(deps.ScannerProxy).WithDataBus(deps.DataBus)
			reg.Register(commands.Command{
				Name: impl.Name(), Usage: impl.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/easm/katana.md",
				Run:             impl.Run, SetProxy: impl.SetProxy, GetProxy: func() string { return impl.Proxy },
			}, "scanner")
		},
	})
}
