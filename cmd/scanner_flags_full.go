//go:build full

package cmd

import (
	katanacmd "github.com/chainreactors/aiscan/pkg/tools/katana"
	passivecmd "github.com/chainreactors/aiscan/pkg/tools/passive"
)

func init() {
	extraScannerUsage["katana"] = func() string { return katanacmd.New().Usage() }
	extraScannerUsage["passive"] = func() string { return passivecmd.New(nil).Usage() }
}
