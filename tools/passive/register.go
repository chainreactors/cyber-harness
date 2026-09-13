//go:build full

// Passive uses the full-only uncover engine API; the standard stub does not
// expose QueryRaw, RawFofa or RawHunter, so this is a real implementation gate.

package passive

import (
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func NewCommand(engines *engine.Set, logger telemetry.Logger) commands.Command {
	var backend QueryEngine
	if engines != nil && engines.Uncover != nil {
		backend = engines.Uncover
	}
	impl := New(backend).WithLogger(logger)
	return commands.Command{
		Name: impl.Name(), Usage: impl.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/passive.md",
		Run:             impl.Run,
	}
}
