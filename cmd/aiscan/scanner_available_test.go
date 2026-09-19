package main

import (
	"slices"

	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
)

func scannerNames() []string {
	names := scannerext.Names()
	slices.Sort(names)
	return names
}
