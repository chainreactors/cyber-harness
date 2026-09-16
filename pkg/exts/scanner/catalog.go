package scanner

import (
	"fmt"

	"github.com/chainreactors/cyber/tools/curl"
	"github.com/chainreactors/cyber/tools/gogo"
	"github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/proton"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/spray"
	"github.com/chainreactors/cyber/tools/zombie"
)

// Metadata is the CLI-facing description owned by a compiled scanner.
type Metadata struct {
	Name        string
	Description string
	Usage       func() string
}

func Catalog() []Metadata {
	result := []Metadata{
		{Name: "curl", Description: "HTTP requests (pure-Go, browser-naturalized)", Usage: func() string { return curl.New().Usage() }},
		{Name: "gogo", Description: "Run gogo directly", Usage: func() string { return gogo.New(nil).Usage() }},
		{Name: "neutron", Description: "Run neutron directly", Usage: func() string { return neutron.New(nil, nil).Usage() }},
		{Name: "proton", Description: "Run proton sensitive info scanner", Usage: func() string { return proton.New().Usage() }},
		{Name: "spray", Description: "Run spray directly", Usage: func() string { return spray.New(nil).Usage() }},
		{Name: "zombie", Description: "Run zombie directly", Usage: func() string { return zombie.New(nil).Usage() }},
		{Name: "scan", Usage: scan.Usage},
	}
	return append(result, editionMetadata()...)
}

func Available(name string) bool {
	_, ok := Lookup(name)
	return ok
}

func Lookup(name string) (Metadata, bool) {
	for _, metadata := range Catalog() {
		if metadata.Name == name {
			return metadata, true
		}
	}
	return Metadata{}, false
}

func Summaries() []string {
	var result []string
	for _, metadata := range Catalog() {
		if metadata.Name != "" {
			result = append(result, metadata.Name)
		}
	}
	return result
}

func UsageLines() []string {
	var result []string
	for _, metadata := range Catalog() {
		if metadata.Description != "" {
			result = append(result, fmt.Sprintf("  %-15s%s", metadata.Name, metadata.Description))
		}
	}
	return result
}
