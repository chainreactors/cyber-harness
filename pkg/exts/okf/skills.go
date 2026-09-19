package okf

import (
	"embed"

	"github.com/chainreactors/cyber/agent/skills"
)

//go:embed assets
var assets embed.FS

func referenceBundle() skills.Bundle {
	return skills.Bundle{ReadVirtual: func(location string) (string, bool, error) {
		if location != ReferenceURI {
			return "", false, nil
		}
		raw, err := assets.ReadFile("assets/okf.md")
		return string(raw), true, err
	}}
}
