package runner

import cfg "github.com/chainreactors/aiscan/core/config"

// ResolveRuntimeConfig resolves the process configuration and applies process
// state such as the data directory.
func ResolveRuntimeConfig(option *cfg.Option) (string, error) {
	return cfg.ResolveRuntimeConfig(option, true)
}

// ResolveRuntimeConfigCandidate resolves a staged Web configuration without
// mutating process-wide state before the candidate is committed.
func ResolveRuntimeConfigCandidate(option *cfg.Option) (string, error) {
	return cfg.ResolveRuntimeConfig(option, false)
}
