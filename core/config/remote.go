package config

// MergeRemoteOption merges remote config into local option. Local (non-empty)
// fields take priority.
func MergeRemoteOption(local *Option, remote *Option) {
	mergeOption(local, remote)
}
