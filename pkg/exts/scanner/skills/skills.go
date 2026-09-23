// Package skills embeds the cybersecurity skill tree owned by the scanner
// extension: the cyber skill, its OKF tool concepts, and the scan workflow
// documents. The content is contributed as a skills.Bundle and mounted for
// virtual reads by the owning extension, so hosts without the scanner carry
// none of it.
package skills

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/chainreactors/cyber/agent/skills"
)

//go:embed all:*
var content embed.FS

// FS exposes the embedded tree for virtual file mounts.
func FS() fs.FS { return content }

// Bundle returns the scanner skill contribution: the cyber skill plus
// virtual reads under cyber://skills/cyber/ and cyber://skills/scan/.
func Bundle() (skills.Bundle, []skills.Diagnostic) {
	raw, err := content.ReadFile("cyber/SKILL.md")
	if err != nil {
		return skills.Bundle{}, []skills.Diagnostic{{Path: "cyber/SKILL.md", Message: err.Error()}}
	}
	fm, _ := skills.ParseFrontmatter(string(raw))
	if fm.Description == "" {
		return skills.Bundle{}, []skills.Diagnostic{{Path: "cyber/SKILL.md", Message: "description is required"}}
	}
	skill := skills.Skill{
		Name:        fm.Name,
		Description: fm.Description,
		Internal:    fm.Internal,
		Source:      skills.SourceBundle,
		Location:    "cyber://skills/cyber/SKILL.md",
		BaseDir:     "cyber://skills/cyber",
	}
	return skills.Bundle{
		Skills:      []skills.Skill{skill},
		ReadVirtual: readVirtual,
	}, nil
}

// readVirtual serves the embedded tree. A miss inside the owned prefixes is
// reported as unhandled so later bundles can claim the same namespace (the
// ioa bundle serves additional documents under cyber://skills/cyber/).
func readVirtual(location string) (string, bool, error) {
	var rel string
	switch {
	case strings.HasPrefix(location, "cyber://skills/cyber/"):
		rel = "cyber/" + strings.TrimPrefix(location, "cyber://skills/cyber/")
	case strings.HasPrefix(location, "cyber://skills/scan/"):
		rel = "scan/" + strings.TrimPrefix(location, "cyber://skills/scan/")
	default:
		return "", false, nil
	}
	data, err := content.ReadFile(rel)
	if err != nil {
		return "", false, nil
	}
	return string(data), true, nil
}
