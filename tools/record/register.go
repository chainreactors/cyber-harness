//go:build full && (windows || linux)

package record

import (
	"os"

	"github.com/chainreactors/aiscan/core/capability"
	coreconfig "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "record", Kind: capability.KindTool, Group: "record",
		Optional: true, Default: true,
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "record",
		Build: func(deps *commands.Deps, reg *commands.CommandRegistry) {
			maxConcurrent, err := maxConcurrentFromEnvironment(os.LookupEnv)
			if err != nil {
				deps.GetLogger().Warnf("record config: %s; using default %d", err, defaultMaxConcurrent)
				maxConcurrent = defaultMaxConcurrent
			}
			reg.RegisterTool(New(
				deps.WorkDir,
				coreconfig.DataSubDir("record"),
				maxConcurrent,
				newPlatformBackend(),
			))
		},
	})
}
