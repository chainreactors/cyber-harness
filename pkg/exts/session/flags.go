package session

import cfg "github.com/chainreactors/aiscan/core/config"
import settings "github.com/chainreactors/aiscan/pkg/cli"

// FlagGroups preserves the typed option schema and its existing defaults.
// Declaration and --help never require an App or a loaded extension.
func FlagGroups(options *cfg.AgentOptions) []cfg.FlagGroup {
	return []cfg.FlagGroup{{Name: "Agent Options", Options: options}}
}

// Declaration exposes Session's CLI surface to the profile settings phase.
// Session flags are inert: they are parsed before the Session extension is
// constructed and never register themselves during Load.
func Declaration(options *cfg.AgentOptions) settings.Declaration {
	return settings.Declaration{
		ID:    "session",
		Flags: []settings.Flag{{Command: "agent", Group: cfg.FlagGroup{Name: "Agent Options", Options: options}}},
	}
}
