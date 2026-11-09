package scanner

import (
	"github.com/chainreactors/cyber/exts/proton"
	"github.com/chainreactors/cyber/tools/curl"
	"github.com/chainreactors/cyber/tools/gogo"
	"github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/spray"
	"github.com/chainreactors/cyber/tools/zombie"
)

func Names() []string {
	return []string{"curl", "gogo", "neutron", "proton", "spray", "zombie", "scan"}
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
		return "", false
	}
}

func Description(name string) string {
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
		return ""
	}
}
