package skills

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/chainreactors/cyber/agent/skills"
)

// RuntimeDocsURI is the virtual prefix of the neutral runtime tool documents
// the library ships for every distribution.
const RuntimeDocsURI = "cyber://skills/runtime/"

//go:embed assets
var assets embed.FS

// RuntimeDocsFS exposes the runtime document shelf for virtual file mounts.
func RuntimeDocsFS() fs.FS {
	sub, err := fs.Sub(assets, "assets/runtime")
	if err != nil {
		panic(err)
	}
	return sub
}

// runtimeDocsBundle serves the runtime documents through the skill store.
// A miss is unhandled so other bundles can share the prefix.
func runtimeDocsBundle() skills.Bundle {
	return skills.Bundle{ReadVirtual: func(location string) (string, bool, error) {
		if !strings.HasPrefix(location, RuntimeDocsURI) {
			return "", false, nil
		}
		data, err := assets.ReadFile("assets/runtime/" + strings.TrimPrefix(location, RuntimeDocsURI))
		if err == nil {
			return string(data), true, nil
		}
		return "", false, nil
	}}
}
