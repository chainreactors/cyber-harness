package commands

import "github.com/chainreactors/aiscan/core/tool"

// ParseArgs forwards to tool.ParseArgs.
// Go type aliases cannot forward generic functions, so this thin wrapper is needed.
func ParseArgs[T any](arguments string) (T, error) {
	return tool.ParseArgs[T](arguments)
}
