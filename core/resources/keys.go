package resources

import "github.com/chainreactors/aiscan/core/deps"

// SetKey carries the loaded scanner resources (fingers, neutron, proton
// configs) to the command factories.
var SetKey = deps.NewKey[*Set]("core.resources.Set")
