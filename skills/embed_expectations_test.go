//go:build !full

package skills

func expectedEmbeddedSkillNames() []string {
	return []string{"aiscan", "ioa", "playwright", "scan", "gogo", "spray", "fuzz", "zombie", "neutron", "sniper", "verify", "report", "web_search", "web_fetch"}
}

func internalPromptSkillNames() []string {
	return []string{"playwright", "scan", "gogo", "spray", "fuzz", "zombie", "neutron", "web_search", "web_fetch", "vision"}
}
