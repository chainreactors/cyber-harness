package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// ReconConfigKey is the passive reconnaissance configuration section owned
// by this extension.
const ReconConfigKey = "recon"

// ReconOptions holds passive reconnaissance credentials and limits. The flag
// surface lives in reconFlags below.
type ReconOptions struct {
	FofaKey      string `config:"fofa_key" json:"fofa_key"`
	HunterAPIKey string `config:"hunter_api_key" json:"hunter_api_key"`
	TavilyKey    string `config:"tavily_key" json:"tavily_key"`
	Proxy        string `config:"proxy" json:"proxy"`
	Limit        *int   `config:"limit" json:"limit"`
}

// ReconSection declares the recon configuration section, including the
// FOFA_KEY / HUNTER_API_KEY / TAVILY_API_KEY / RECON_PROXY environment
// mapping.
func ReconSection() cfg.Section {
	return cfg.Section{
		Key: ReconConfigKey, Aliases: []string{ReconConfigKey},
		New:     func() any { return &ReconOptions{} },
		Secrets: []string{"fofa_key", "hunter_api_key", "tavily_key"},
		Environment: func(sources cfg.Sources) (map[string]any, map[string]any, error) {
			overrides := map[string]any{}
			set := func(field string, name string) {
				if _, explicit := sources.CLI[field]; explicit {
					return
				}
				if value, ok := sources.LookupEnv(name); ok && value != "" {
					overrides[field] = value
				}
			}
			set("fofa_key", "FOFA_KEY")
			set("hunter_api_key", "HUNTER_API_KEY")
			set("tavily_key", "TAVILY_API_KEY")
			set("proxy", "RECON_PROXY")
			if len(overrides) == 0 {
				return nil, nil, nil
			}
			return overrides, nil, nil
		},
	}
}

// ReadRecon decodes the recon section from a parsed option set.
func ReadRecon(option *cfg.Option) (ReconOptions, error) {
	if option == nil {
		return ReconOptions{}, nil
	}
	if option.Resolved != nil {
		decoded, err := cfg.Get[*ReconOptions](option.Resolved, ReconConfigKey)
		if err != nil {
			return ReconOptions{}, err
		}
		return *decoded, nil
	}
	registry := cfg.NewSections()
	if _, err := registry.Add(ReconSection()); err != nil {
		return ReconOptions{}, err
	}
	raw, err := registry.Decode(ReconConfigKey, option.Extensions[ReconConfigKey])
	if err != nil {
		return ReconOptions{}, err
	}
	return *raw.(*ReconOptions), nil
}

// reconFlags mirrors ReconOptions for the CLI. Full editions show the group
// in help output; other editions parse the same flags with the group hidden.
type reconFlags struct {
	FofaKey      string `long:"fofa-key" config:"fofa_key" description:"FOFA API key for passive recon (or set env FOFA_KEY)"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" description:"Hunter API key (64-hex from console) (or env HUNTER_API_KEY)"`
	TavilyKey    string `long:"tavily-key" config:"tavily_key" description:"Tavily API key for web search (or env TAVILY_API_KEY)"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" description:"Outbound proxy for passive recon (socks5://host:port for hunter via mainland)"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" description:"Per-query asset limit for passive recon (0 = unlimited)"`
}

// ReconFlagGroup contributes the recon flags to a command.
func ReconFlagGroup() cfg.FlagGroup {
	return cfg.FlagGroup{Name: "Recon Options", Options: &reconFlags{}, Hidden: !fullEdition}
}
