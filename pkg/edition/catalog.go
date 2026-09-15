// Package edition defines the explicit feature catalog linked into Cyber.
// Build tags choose the descriptor list; package initialization has no effect
// on any other profile or process-wide state.
package edition

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/tools/curl"
	"github.com/chainreactors/cyber/tools/gogo"
	"github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/proton"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/spray"
	"github.com/chainreactors/cyber/tools/zombie"
)

func Catalog(extra ...capability.Descriptor) capability.Catalog {
	descriptors := []capability.Descriptor{
		{ID: "core", Kind: capability.KindTool, Group: "core"},
		{ID: "arsenal", Kind: capability.KindTool, Group: "arsenal"},
		{ID: "search", Kind: capability.KindTool, Group: "search", Optional: true, Default: true},
		{ID: "proxy", Kind: capability.KindService, Group: "proxy"},
		{ID: "curl", Kind: capability.KindScanner, Group: "scanner", CLIName: "curl", Summary: "curl", UsageLine: "  curl           HTTP requests (pure-Go, browser-naturalized)", Usage: func() string { return curl.New().Usage() }},
		{ID: "gogo", Kind: capability.KindScanner, Group: "scanner", CLIName: "gogo", Summary: "gogo", UsageLine: "  gogo           Run gogo directly", Usage: func() string { return gogo.New(nil).Usage() }},
		{ID: "neutron", Kind: capability.KindScanner, Group: "scanner", CLIName: "neutron", Summary: "neutron", UsageLine: "  neutron        Run neutron directly", Usage: func() string { return neutron.New(nil, nil).Usage() }},
		{ID: "proton", Kind: capability.KindScanner, Group: "scanner", CLIName: "proton", Summary: "proton", UsageLine: "  proton         Run proton sensitive info scanner", Usage: func() string { return proton.New().Usage() }},
		{ID: "spray", Kind: capability.KindScanner, Group: "scanner", CLIName: "spray", Summary: "spray", UsageLine: "  spray          Run spray directly", Usage: func() string { return spray.New(nil).Usage() }},
		{ID: "zombie", Kind: capability.KindScanner, Group: "scanner", CLIName: "zombie", Summary: "zombie", UsageLine: "  zombie         Run zombie directly", Usage: func() string { return zombie.New(nil).Usage() }},
		{ID: "scan", Kind: capability.KindScanner, Group: "scanner", CLIName: "scan", Summary: "scan", Usage: scan.Usage},
	}
	descriptors = append(descriptors, platformCapabilities()...)
	descriptors = append(descriptors, extra...)
	return capability.Must(descriptors...)
}
