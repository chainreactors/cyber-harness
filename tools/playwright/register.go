//go:build full

package playwright

import "github.com/chainreactors/aiscan/pkg/commands"

func init() {
	commands.RegisterFactory(commands.Factory{
		Group: "browser",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			impl := New(deps.WorkDir)
			reg.Register(commands.Command{Name: impl.Name(), Usage: impl.Usage(), Run: impl.Run, Close: impl.Close}, "browser")
		},
	})
}
