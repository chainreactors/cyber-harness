package search

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// ConfigKey is the configuration section owned by this extension.
const ConfigKey = "search"

// DefaultTavilyKeys is the link-time default for the search section.
var DefaultTavilyKeys = ""

// Options holds web search configuration. It carries no flags.
type Options struct {
	TavilyKeys string `config:"tavily_keys" json:"tavily_keys" description:"Tavily API keys (comma-separated; empty falls back to DuckDuckGo)"`
}

// Section declares the search configuration section.
func Section() cfg.Section {
	return cfg.Section{
		Key: ConfigKey, Aliases: []string{ConfigKey},
		New:     func() any { return &Options{TavilyKeys: DefaultTavilyKeys} },
		Secrets: []string{"tavily_keys"},
	}
}

// ReadKeys decodes the tavily keys from a parsed option set.
func ReadKeys(option *cfg.Option) (string, error) {
	if option == nil {
		return DefaultTavilyKeys, nil
	}
	if option.Resolved != nil {
		decoded, err := cfg.Get[*Options](option.Resolved, ConfigKey)
		if err != nil {
			return "", err
		}
		return decoded.TavilyKeys, nil
	}
	registry := cfg.NewSections()
	if _, err := registry.Add(Section()); err != nil {
		return "", err
	}
	raw, err := registry.Decode(ConfigKey, option.Extensions[ConfigKey])
	if err != nil {
		return "", err
	}
	return raw.(*Options).TavilyKeys, nil
}
