package capability

var gatedSkills = map[string]ID{"katana": "katana", "passive": "passive"}

func (c Catalog) SkillEnabled(name string) bool {
	id, gated := gatedSkills[name]
	return !gated || c.Enabled(id)
}
