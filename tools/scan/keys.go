package scan

import "github.com/chainreactors/aiscan/core/deps"

// OptsKey carries scan options built by the assembly layer (parent agent, deep
// browser, skill reader) that cannot be expressed as plain Deps fields.
var OptsKey = deps.NewKey[[]Option]("scan.Options")
