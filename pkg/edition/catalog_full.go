//go:build full

package edition

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/tools/katana"
	"github.com/chainreactors/aiscan/tools/passive"
)

func platformCapabilities() []capability.Descriptor {
	result := []capability.Descriptor{
		{ID: "browser", Kind: capability.KindTool, Group: "browser", Optional: true, Default: true},
		{ID: "katana", Kind: capability.KindScanner, Group: "scanner", CLIName: "katana", Summary: "katana", UsageLine: "  katana         Run katana web crawler", Usage: func() string { return katana.New().Usage() }, Skills: []string{"katana"}},
		{ID: "passive", Kind: capability.KindScanner, Group: "scanner", CLIName: "passive", Summary: "passive", UsageLine: "  passive        Run passive cyberspace recon", Usage: func() string { return passive.New(nil).Usage() }, Skills: []string{"passive"}},
	}
	return append(result, recorderCapability()...)
}
