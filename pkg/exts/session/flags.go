package session

import cfg "github.com/chainreactors/aiscan/core/config"

// FlagGroups preserves the typed option schema and its existing defaults.
// Declaration and --help never require an App or a loaded extension.
func FlagGroups(options *cfg.AgentOptions) []cfg.FlagGroup {
	return []cfg.FlagGroup{{Name: "Agent Options", Options: options}}
}
