package vision

import (
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/provider"
)

func init() {
	command.RegisterFactory(command.Factory{
		Group: "tools",
		Build: func(deps *command.Deps, reg *command.CommandRegistry) {
			cfg, _ := deps.VisionConfig.(*provider.ProviderConfig)
			if cfg == nil {
				return
			}
			reg.Register(New(cfg), "tools")
		},
	})
}
