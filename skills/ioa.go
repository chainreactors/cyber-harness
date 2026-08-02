package skills

import (
	"fmt"
	"strings"

	ioaskills "github.com/chainreactors/ioa/skills"
)

const ioaURIPrefix = "ioa://skills/"

// loadIOAModuleSkills loads the protocol skills embedded in the
// chainreactors/ioa module (checkpoint, handoff, swarm, team). They are the
// canonical wire-protocol definitions for IOA messages.
func loadIOAModuleSkills() ([]Skill, []Diagnostic) {
	loaded, err := ioaskills.LoadAll()
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("read ioa module skills: %s", err)}}
	}
	out := make([]Skill, 0, len(loaded))
	for _, s := range loaded {
		out = append(out, Skill{
			Name:        s.Name,
			Description: s.Description,
			Internal:    true,
			Source:      SourceIOA,
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
