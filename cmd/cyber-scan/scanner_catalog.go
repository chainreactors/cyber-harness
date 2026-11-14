package main

import (
	"fmt"
	"slices"

	"github.com/chainreactors/cyber/core/resource"
	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
)

func productScannerNames() []string            { return append(scannerext.Names(), discoveryNames()...) }
func productScannerAvailable(name string) bool { return slices.Contains(productScannerNames(), name) }
func productScannerUsage(name string) (string, bool) {
	if usage, ok := scannerext.Usage(name); ok {
		return usage, true
	}
	return discoveryUsage(name)
}
func productScannerDescription(name string) string {
	if description := scannerext.Description(name); description != "" {
		return description
	}
	return discoveryDescription(name)
}
func productScannerUsageLines() []string {
	var out []string
	for _, name := range productScannerNames() {
		if description := productScannerDescription(name); description != "" {
			out = append(out, fmt.Sprintf("  %-15s%s", name, description))
		}
	}
	return out
}
func resourceAddReconFlags(resources *resource.Registry) error {
	_, err := resource.Add[hostcli.Contribution](resources, func(registry *hostcli.Registry) error {
		return registry.Group("agent", passiveconfig.ReconConfigKey, passiveconfig.ReconFlagGroup())
	})
	return err
}
