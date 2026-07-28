package engine

import "github.com/chainreactors/aiscan/core/deps"

// SetKey carries the initialized scanner engines to the command factories.
// Declared here so pkg/commands never has to link the scanner SDKs.
var SetKey = deps.NewKey[*Set]("scan.engine.Set")
