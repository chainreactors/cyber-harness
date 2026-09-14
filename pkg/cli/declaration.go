package cli

import "github.com/chainreactors/aiscan/core/config"

// Declaration is a capability's inert contribution to the settings provider.
// Contributors depend on this contract, not on its lifecycle implementation.
type Declaration struct {
	ID         string
	Config     []config.Section
	Flags      []Flag
	DeclareCLI func(*Registry) error
}

type Flag struct {
	Command string
	Key     string
	Group   config.FlagGroup
}
