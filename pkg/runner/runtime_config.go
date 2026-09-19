package runner

import cfg "github.com/chainreactors/cyber/core/config"

// ResolveRuntimeConfig resolves the process configuration and applies process
// state such as the data directory.
func ResolveRuntimeConfig(option *cfg.Option) (string, error) {
	return cfg.ResolveRuntimeConfig(option)
}
