package client

import (
	"embed"
	"fmt"
	"github.com/chainreactors/cyber/agent/skills"
	"strings"

	ioaskills "github.com/chainreactors/ioa/skills"
)

const ioaURIPrefix = "ioa://skills/"

// loadIOAModuleSkills loads the protocol skills embedded in the
// chainreactors/ioa module (checkpoint, handoff, swarm, team). They are the
// canonical wire-protocol definitions for IOA messages.
func loadIOAModuleSkills() ([]skills.Skill, []skills.Diagnostic) {
	loaded, err := ioaskills.LoadAll()
	if err != nil {
		return nil, []skills.Diagnostic{{Message: fmt.Sprintf("read ioa module skills: %s", err)}}
	}
	out := make([]skills.Skill, 0, len(loaded))
	for _, s := range loaded {
		out = append(out, skills.Skill{
			Name:        s.Name,
			Description: s.Description,
			Internal:    true,
			Source:      skills.SourceBundle,
			Location:    ioaURIPrefix + s.Name + "/SKILL.md",
			BaseDir:     ioaURIPrefix + s.Name,
		})
	}
	return out, nil
}

// readIOAVirtual reads ioa://skills/<name>/<file> from the ioa module's
// embedded filesystem. Supports SKILL.md and schema.json.
func readIOAVirtual(location string) (string, bool, error) {
	rest := strings.TrimPrefix(location, ioaURIPrefix)
	name, file, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return "", false, nil
	}
	var raw []byte
	var err error
	switch file {
	case "SKILL.md":
		raw, err = ioaskills.ReadSkillRaw(name)
	case "schema.json":
		raw, err = ioaskills.ReadSchemaRaw(name)
	default:
		return "", true, fmt.Errorf("ioa virtual file not found: %s", location)
	}
	if err != nil {
		return "", true, fmt.Errorf("ioa virtual file not found: %s", location)
	}
	return string(raw), true, nil
}

//go:embed SKILL.md assets
var skillFS embed.FS

// Skills returns the client's inert, read-only skill contribution.
func Skills() (skills.Bundle, []skills.Diagnostic) {
	values, diagnostics := loadIOAModuleSkills()
	raw, err := skillFS.ReadFile("SKILL.md")
	if err != nil {
		return skills.Bundle{}, append(diagnostics, skills.Diagnostic{Message: err.Error()})
	}
	fm, _ := skills.ParseFrontmatter(string(raw))
	values = append(values, skills.Skill{Name: fm.Name, Description: fm.Description, Internal: true, Source: skills.SourceBundle, Location: "cyber://skills/ioa/SKILL.md", BaseDir: "cyber://skills/ioa"})
	return skills.Bundle{Skills: values, ReadVirtual: func(location string) (string, bool, error) {
		if location == "cyber://skills/ioa/SKILL.md" {
			return string(raw), true, nil
		}
		if strings.HasPrefix(location, "cyber://skills/cyber/") {
			name := "assets/" + strings.TrimPrefix(location, "cyber://skills/")
			data, err := skillFS.ReadFile(name)
			if err == nil {
				return string(data), true, nil
			}
		}
		if strings.HasPrefix(location, ioaURIPrefix) {
			return readIOAVirtual(location)
		}
		return "", false, nil
	}}, diagnostics
}
