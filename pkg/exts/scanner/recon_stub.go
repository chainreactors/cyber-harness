//go:build !full

package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// reconFlags mirrors ReconOptions with the flag surface hidden outside full
// editions; the configuration keys still parse.
type reconFlags struct {
	FofaKey      string `long:"fofa-key" config:"fofa_key" hidden:"true"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" hidden:"true"`
	TavilyKey    string `long:"tavily-key" config:"tavily_key" hidden:"true"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" hidden:"true"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" hidden:"true"`
}

// ReconFlagGroup contributes the recon flags to a command.
func ReconFlagGroup() cfg.FlagGroup {
	return cfg.FlagGroup{Name: "Recon Options", Options: &reconFlags{}}
}
