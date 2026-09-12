//go:build !full

package app

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func editionToolEntries(*App, Config, capability.Plan) ([]extension.Entry, error)        { return nil, nil }
func registerEditionScanners(*App, capability.Plan, *engine.Set, telemetry.Logger) error { return nil }
