package scanner

import (
	"fmt"

	"github.com/chainreactors/cyber/pkg/exts/proton"
	"github.com/chainreactors/cyber/tools/curl"
	"github.com/chainreactors/cyber/tools/gogo"
	"github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/spray"
	"github.com/chainreactors/cyber/tools/zombie"
)

func Names() []string {
	return append([]string{"curl", "gogo", "neutron", "proton", "spray", "zombie", "scan"}, manifestNames()...)
}

func Available(name string) bool {
	for _, available := range Names() {
		if available == name {
			return true
		}
	}
	return false
}

func Usage(name string) (string, bool) {
	switch name {
	case "curl":
		return curl.New().Usage(), true
	case "gogo":
		return gogo.New(nil).Usage(), true
	case "neutron":
		return neutron.New(nil, nil).Usage(), true
	case "proton":
		return proton.Usage(), true
	case "spray":
		return spray.New(nil).Usage(), true
	case "zombie":
		return zombie.New(nil).Usage(), true
	case "scan":
		return scan.Usage(), true
	default:
		return manifestUsage(name)
	}
}

func UsageLines() []string {
	var result []string
	for _, name := range Names() {
		if description := scannerDescription(name); description != "" {
			result = append(result, fmt.Sprintf("  %-15s%s", name, description))
		}
	}
	return result
}

func Description(name string) string {
	return scannerDescription(name)
}

func scannerDescription(name string) string {
	switch name {
	case "curl":
		return "HTTP requests (pure-Go, browser-naturalized)"
	case "gogo":
		return "Run gogo directly"
	case "neutron":
		return "Run neutron directly"
	case "proton":
		return "Run proton sensitive info scanner"
	case "spray":
		return "Run spray directly"
	case "zombie":
		return "Run zombie directly"
	default:
		return manifestDescription(name)
	}
}
