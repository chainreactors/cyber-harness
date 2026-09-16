package main

import (
	"slices"

	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
)

func scannerNames() []string {
	metadata := scannerext.Catalog()
	names := make([]string, 0, len(metadata))
	for _, item := range metadata {
		names = append(names, item.Name)
	}
	slices.Sort(names)
	return names
}
