//go:build !full

package edition

import "github.com/chainreactors/cyber/core/capability"

func platformCapabilities() []capability.Descriptor { return nil }
