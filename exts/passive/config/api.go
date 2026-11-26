package config

import (
	"fmt"
	"sort"
	"strings"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

var apiKeys = map[string]string{
	"hunter": "HUNTER_API_KEY", "shodan": "SHODAN_API_KEY", "shodan-idb": "",
	"censys": "CENSYS_API_TOKEN", "quake": "QUAKE_TOKEN", "zoomeye": "ZOOMEYE_API_KEY",
	"netlas": "NETLAS_API_KEY", "criminalip": "CRIMINALIP_API_KEY", "publicwww": "PUBLICWWW_API_KEY",
	"hunterhow": "HUNTERHOW_API_KEY", "odin": "ODIN_API_KEY", "binaryedge": "BINARYEDGE_API_KEY",
	"onyphe": "ONYPHE_API_KEY", "driftnet": "DRIFTNET_API_KEY", "greynoise": "GREYNOISE_API_KEY",
}

// Sections declares every API used by the passive command. The dependency's
// native clients retain their paths, authentication and response parsing.
func Sections() []cfg.Section {
	names := make([]string, 0, len(apiKeys))
	for name := range apiKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	sections := []cfg.Section{FofaSection()}
	for _, name := range names {
		sections = append(sections, apiSection(name))
	}
	return sections
}

func apiSection(name string) cfg.Section {
	return cfg.Section{Key: name, New: func() any { return &cfg.API{} }, Secrets: []string{"key"}, Normalize: func(value any) error {
		// A blank optional key is omitted: the host credential still applies.
		value.(*cfg.API).Key = strings.TrimSpace(value.(*cfg.API).Key)
		return nil
	}, Validate: func(value any) error {
		api := value.(*cfg.API)
		if api.Key != "" && apiKeys[name] == "" {
			return fmt.Errorf("%s does not use an API key", name)
		}
		return cfg.ValidateBaseURL(api.BaseURL)
	}}
}

// Credential names the original provider credential consumed by Uncover.
// Censys additionally uses CENSYS_ORGANIZATION_ID from the host configuration.
func Credential(source string) string { return apiKeys[source] }
