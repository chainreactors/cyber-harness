//go:build full

package scanner

import (
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
)

func editionNames() []string { return []string{"katana", "passive"} }

func editionUsage(name string) (string, bool) {
	switch name {
	case "katana":
		return katana.New().Usage(), true
	case "passive":
		return passive.New(nil).Usage(), true
	default:
		return "", false
	}
}

func editionDescription(name string) string {
	switch name {
	case "katana":
		return "Run katana web crawler"
	case "passive":
		return "Run passive cyberspace recon"
	default:
		return ""
	}
}
