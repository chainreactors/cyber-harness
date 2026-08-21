package curl

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "curl", Kind: capability.KindScanner, Group: "scanner",
		CLIName: "curl", Summary: "curl", UsageLine: "  curl           HTTP requests (pure-Go, browser-naturalized)",
		Usage: func() string { return New().Usage() },
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "curl",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			impl := New().WithLogger(d.GetLogger()).WithProxy(d.ScannerProxy).WithEvents(d.Events)
			reg.Register(commands.Command{
				Name: impl.Name(), Usage: impl.Usage(), QuickReference: impl.QuickReference(),
				DescriptionPath: "aiscan://skills/aiscan/okf/easm/curl.md",
				Run:             impl.Run, SetProxy: impl.SetProxy, GetProxy: func() string { return impl.Proxy },
			}, "scanner")
		},
	})
}
