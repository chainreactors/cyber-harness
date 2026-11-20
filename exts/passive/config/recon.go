package config

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// ConfigKey is the passive reconnaissance configuration section owned
// by this extension.
const ConfigKey = "recon"

// Recon holds passive reconnaissance credentials and limits.
type Recon struct {
	Service      string `no-flag:"true" json:"service"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" json:"hunter_api_key" description:"Hunter API key (64-hex from console) (or env HUNTER_API_KEY)"`
	TavilyKey    string `long:"tavily-key" config:"tavily_key" json:"tavily_key" description:"Tavily API key for web search (or env TAVILY_API_KEY)"`
	Proxy        string `long:"recon-proxy" config:"proxy" json:"proxy" description:"Outbound proxy for passive recon (socks5://host:port for hunter via mainland)"`
	Limit        *int   `long:"recon-limit" config:"limit" json:"limit" description:"Per-query asset limit for passive recon (0 = unlimited)"`
}

// Section declares the recon configuration section, including the
// HUNTER_API_KEY / TAVILY_API_KEY / RECON_PROXY environment
// mapping.
func Section() cfg.Section {
	return cfg.Section{
		Key:     ConfigKey,
		New:     func() any { return &Recon{} },
		Secrets: []string{"hunter_api_key", "tavily_key"},
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

// Read decodes the recon section from a parsed option set.
func Read(option *cfg.Option) (Recon, error) {
	if option == nil {
		return Recon{}, nil
	}
	if option.Resolved != nil {
		decoded, err := cfg.Get[*Recon](option.Resolved, ConfigKey)
		if err != nil {
			return Recon{}, err
		}
		value := *decoded
		if value.Limit != nil {
			copy := *value.Limit
			value.Limit = &copy
		}
		return value, nil
	}
	raw, err := Section().Decode(option.Extensions[ConfigKey])
	if err != nil {
		return Recon{}, err
	}
	return *raw.(*Recon), nil
}

// Flags contributes the recon flags to a command.
func Flags() cfg.FlagGroup {
	return cfg.FlagGroup{Name: "Recon Options", Options: &Recon{}}
}
