//go:build !full

package edition

import "github.com/chainreactors/aiscan/core/capability"

func platformCapabilities() []capability.Descriptor { return nil }
