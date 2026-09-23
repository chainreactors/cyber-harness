package scanner

import "strings"

// UncoverCredentialEnvNames are the environment variables mapped to per-engine
// asset-discovery credentials.
var UncoverCredentialEnvNames = []string{
	"SHODAN_API_KEY",
	"QUAKE_TOKEN",
	"NETLAS_API_KEY",
	"CRIMINALIP_API_KEY",
	"PUBLICWWW_API_KEY",
	"HUNTERHOW_API_KEY",
	"ZOOMEYE_API_KEY",
	"DRIFTNET_API_KEY",
	"DAYDAYMAP_API_KEY",
	"CENSYS_API_TOKEN",
	"CENSYS_ORGANIZATION_ID",
	"GOOGLE_API_KEY",
	"GOOGLE_API_CX",
	"ODIN_API_KEY",
	"BINARYEDGE_API_KEY",
	"ONYPHE_API_KEY",
	"GREYNOISE_API_KEY",
	"NERDYDATA_API_KEY",
}

// UncoverCredentials derives engine credentials from the environment at
// assembly time; nothing is cached on the parsed option set.
func UncoverCredentials(lookup func(string) (string, bool)) map[string]string {
	if lookup == nil {
		return nil
	}
	var out map[string]string
	for _, name := range UncoverCredentialEnvNames {
		value, ok := lookup(name)
		if !ok {
			continue
		}
		if value = strings.TrimSpace(value); value == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = value
	}
	return out
}
