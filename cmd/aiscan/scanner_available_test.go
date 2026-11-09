package main

import "testing"

func TestCLIParserDeclaresAvailableScanners(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range productScannerNames() {
		if parser.Find(name) == nil {
			t.Errorf("available scanner %q is missing from CLI commands", name)
		}
	}
}
