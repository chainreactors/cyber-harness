//go:build !(record && cgo && (windows || linux))

package record

import "github.com/chainreactors/cyber/core/resource"

// Declare contributes nothing in a build that cannot run recordings. See the
// tagged file for why the section tracks the extension.
func Declare(*resource.Registry) error { return nil }
