//go:build full

package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// reconFlags mirrors ReconOptions with the flag surface visible in full
// editions.
type reconFlags struct {
	FofaKey      string `long:"fofa-key" config:"fofa_key" description:"FOFA API key for passive recon (or set env FOFA_KEY)"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" description:"Hunter API key (64-hex from console) (or env HUNTER_API_KEY)"`
	TavilyKey    string `long:"tavily-key" config:"tavily_key" description:"Tavily API key for web search (or env TAVILY_API_KEY)"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" description:"Outbound proxy for passive recon (socks5://host:port for hunter via mainland)"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" description:"Per-query asset limit for passive recon (0 = unlimited)"`
}

// ReconFlagGroup contributes the recon flags to a command.
func ReconFlagGroup() cfg.FlagGroup {
	return cfg.FlagGroup{Name: "Recon Options", Options: &reconFlags{}}
}
