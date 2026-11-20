package config

import "strings"

// UncoverCredentials derives engine credentials from the environment at
// assembly time; nothing is cached on the parsed option set.
func UncoverCredentials(lookup func(string) (string, bool)) map[string]string {
	if lookup == nil {
		return nil
	}
	names := []string{
		"DAYDAYMAP_API_KEY",
		"CENSYS_ORGANIZATION_ID",
		"GOOGLE_API_KEY",
		"GOOGLE_API_CX",
		"NERDYDATA_API_KEY",
	}
	for source, name := range apiKeys {
		if source != "hunter" && name != "" {
			names = append(names, name)
		}
	}
	var out map[string]string
	for _, name := range names {
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
