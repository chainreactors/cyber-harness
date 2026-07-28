package capability

// gatedSkills maps a skill that ships in the embedded skill set to the
// capability that makes it usable. A skill is hidden unless its capability is
// linked — the embedded FS is the same in every edition, so this table (not a
// build tag) is what keeps the standard build from advertising skills whose
// tools it cannot run.
var gatedSkills = map[string]ID{
	"katana":  "katana",
	"passive": "passive",
}

// SkillEnabled reports whether a skill should be visible in this binary.
func SkillEnabled(name string) bool {
	id, gated := gatedSkills[name]
	if !gated {
		return true
	}
	return Enabled(id)
}

// GatedSkills lists the skill names that depend on a capability being linked.
func GatedSkills() []string {
	out := make([]string, 0, len(gatedSkills))
	for name := range gatedSkills {
		out = append(out, name)
	}
	return out
}
