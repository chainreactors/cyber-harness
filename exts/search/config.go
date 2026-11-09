package search

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// ConfigKey is the configuration section owned by this extension.
const ConfigKey = "search"

// DefaultTavilyKeys is the link-time default for the search section.
// Startup overrides use SetDefaultKeys.
var DefaultTavilyKeys = ""

// SetDefaultKeys replaces the link-time search credentials and returns a
// restore function. Call only during startup, before configuration is resolved.
func SetDefaultKeys(keys string) func() {
	old := DefaultTavilyKeys
	DefaultTavilyKeys = keys
	return func() { DefaultTavilyKeys = old }
}

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
	raw, err := Section().Decode(option.Extensions[ConfigKey])
	if err != nil {
		return "", err
	}
	return raw.(*Options).TavilyKeys, nil
}
