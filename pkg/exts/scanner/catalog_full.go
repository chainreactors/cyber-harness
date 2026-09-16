//go:build full

package scanner

import (
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
)

func editionMetadata() []Metadata {
	return []Metadata{
		{Name: "katana", Description: "Run katana web crawler", Usage: func() string { return katana.New().Usage() }},
		{Name: "passive", Description: "Run passive cyberspace recon", Usage: func() string { return passive.New(nil).Usage() }},
	}
}
