//go:build !full

package skills

func expectedEmbeddedSkillNames() []string {
	return []string{"aiscan", "ioa", "browser", "scan", "gogo", "spray", "fuzz", "zombie", "neutron", "sniper", "swarm", "verify", "report", "web_search", "web_fetch", "vision", "parse_results", "filter_results"}
}

func internalPromptSkillNames() []string {
	return []string{"browser", "scan", "gogo", "spray", "fuzz", "zombie", "neutron", "web_search", "web_fetch", "vision", "parse_results", "filter_results"}
}
