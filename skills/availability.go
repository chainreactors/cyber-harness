package skills

import "github.com/chainreactors/aiscan/core/capability"

func skillAvailable(name string) bool {
	return capability.SkillEnabled(name)
}
