package config

import (
	"os"
	"strings"
)

type envLookup func(string) (string, bool)

// ResolveRuntimeConfig resolves parsed configuration with environment and
// defaults. Provider inference is supplied by the integration layer so config
// remains independent of concrete LLM implementations.
func ResolveRuntimeConfig(option *Option, applyProcessState bool, inferProvider func(string) string) (string, error) {
	explicit := *option
	configPath, err := LoadAndApplyConfig(option)
	if err != nil {
		return configPath, err
	}
	applyEnvironment(option, explicit, os.LookupEnv, inferProvider)
	normalizeProviderOptions(option, inferProvider)
	ApplyDefaults(option)
	if _, err := ResolveOutputPolicy(option); err != nil {
		return configPath, err
	}
	if applyProcessState && strings.TrimSpace(option.DataDir) != "" {
		SetDataDir(option.DataDir)
	}
	return configPath, nil
}

func applyEnvironment(option *Option, explicit Option, lookup envLookup, inferProvider func(string) string) {
	applyLLMEnvironment(option, explicit, lookup, inferProvider)
	applyScannerEnvironment(option, explicit, lookup)
	applyReconEnvironment(option, explicit, lookup)
	applyRuntimeEnvironment(option, explicit, lookup)
}

func applyLLMEnvironment(option *Option, explicit Option, lookup envLookup, inferProvider func(string) string) {
	providerExplicit := strings.TrimSpace(explicit.Provider) != ""
	if v := firstEnv(lookup, "AISCAN_PROVIDER", "AISCAN_LLM_PROVIDER"); v != "" && !providerExplicit {
		option.Provider = v
	}

	selectedProvider := selectedEnvProvider(option, lookup, inferProvider)
	if option.Provider == "" && selectedProvider != "" && !providerExplicit {
		option.Provider = selectedProvider
	}

	// AISCAN_BASE_URL is aiscan's own namespace: an intentional override that wins
	// over a base URL set in the config file (CLI --base-url wins via the explicit gate).
	if strings.TrimSpace(explicit.BaseURL) == "" {
		if v := firstEnv(lookup, "AISCAN_BASE_URL", "AISCAN_BASEURL", "AISCAN_LLM_BASE_URL", "AISCAN_LLM_BASEURL"); v != "" {
			option.BaseURL = v
		}
	}
	// Provider-scoped base-URL envs (ANTHROPIC_BASE_URL, OPENAI_BASE_URL, …) are
	// commonly injected by the surrounding environment for *other* tools — e.g.
	// Claude-Code-style gateways export ANTHROPIC_BASE_URL. Treat them as a fallback
	// only: they must not silently override a base URL the user configured for aiscan
	// (config file / Settings UI). Apply only when nothing else set one. Mirrors the
	// model handling below so a hub-launched agent that inherits the hub's env still
	// honors the Settings-saved base URL.
	if strings.TrimSpace(option.BaseURL) == "" {
		if v := providerBaseURLEnv(selectedProvider, lookup); v != "" {
			option.BaseURL = v
		}
	}

	// AISCAN_MODEL / AISCAN_LLM_MODEL are aiscan's *own* namespace: an intentional
	// override that still wins over a model set in the config file (CLI --model
	// wins over it via the explicit gate).
	if strings.TrimSpace(explicit.Model) == "" {
		if v := firstEnv(lookup, "AISCAN_MODEL", "AISCAN_LLM_MODEL"); v != "" {
			option.Model = v
		}
	}
	// Provider-scoped model envs (ANTHROPIC_MODEL, OPENAI_MODEL, …) are commonly
	// injected by the surrounding environment for *other* tools — e.g. Claude-Code
	// style gateways export ANTHROPIC_MODEL. Treat them as a fallback only: they
	// must not silently override a model the user configured for aiscan (config
	// file / Settings UI or --model). Apply only when nothing else set a model.
	if strings.TrimSpace(option.Model) == "" {
		if v := providerModelEnv(selectedProvider, lookup); v != "" {
			option.Model = v
		}
	}

	// AISCAN_API_KEY is aiscan's own namespace: an intentional override that wins
	// over a key set in the config file (CLI --api-key wins via the explicit gate).
	if strings.TrimSpace(explicit.APIKey) == "" {
		if v := firstEnv(lookup, "AISCAN_API_KEY", "AISCAN_LLM_API_KEY"); v != "" {
			option.APIKey = v
		}
	}
	// Provider-scoped key envs (ANTHROPIC_API_KEY, OPENAI_API_KEY) are commonly
	// present for *other* tools. Treat them as a fallback only so they never override
	// a key the user configured for aiscan (config file / Settings UI). Apply only
	// when nothing else set one — same rationale as base URL and model above.
	if strings.TrimSpace(option.APIKey) == "" {
		if v := providerAPIKeyEnv(selectedProvider, lookup); v != "" {
			option.APIKey = v
		}
	}

	if strings.TrimSpace(explicit.LLMProxy) == "" {
		if v := firstEnv(lookup, "AISCAN_LLM_PROXY"); v != "" {
			option.LLMProxy = v
		}
	}
}

func applyScannerEnvironment(option *Option, explicit Option, lookup envLookup) {
	if strings.TrimSpace(explicit.CyberhubURL) == "" {
		if v := firstEnv(lookup, "AISCAN_CYBERHUB_URL", "CYBERHUB_URL"); v != "" {
			option.CyberhubURL = v
		}
	}
	if strings.TrimSpace(explicit.CyberhubKey) == "" {
		if v := firstEnv(lookup, "AISCAN_CYBERHUB_KEY", "CYBERHUB_KEY"); v != "" {
			option.CyberhubKey = v
		}
	}
	if strings.TrimSpace(explicit.CyberhubMode) == "" {
		if v := firstEnv(lookup, "AISCAN_CYBERHUB_MODE", "CYBERHUB_MODE"); v != "" {
			option.CyberhubMode = v
		}
	}
	if strings.TrimSpace(explicit.Proxy) == "" {
		if v := firstEnv(lookup, "AISCAN_PROXY", "AISCAN_SCANNER_PROXY"); v != "" {
			option.Proxy = v
		}
	}
}

func applyReconEnvironment(option *Option, explicit Option, lookup envLookup) {
	if strings.TrimSpace(explicit.FofaEmail) == "" {
		if v := firstEnv(lookup, "FOFA_EMAIL"); v != "" {
			option.FofaEmail = v
		}
	}
	if strings.TrimSpace(explicit.FofaKey) == "" {
		if v := firstEnv(lookup, "FOFA_KEY"); v != "" {
			option.FofaKey = v
		}
	}
	if strings.TrimSpace(explicit.HunterToken) == "" {
		if v := firstEnv(lookup, "HUNTER_TOKEN"); v != "" {
			option.HunterToken = v
		}
	}
	if strings.TrimSpace(explicit.HunterAPIKey) == "" {
		if v := firstEnv(lookup, "HUNTER_API_KEY"); v != "" {
			option.HunterAPIKey = v
		}
	}
	if strings.TrimSpace(explicit.TavilyKey) == "" {
		if v := firstEnv(lookup, "TAVILY_API_KEY", "TAVILY_API_KEYS"); v != "" {
			option.TavilyKey = v
		}
	}
	if strings.TrimSpace(explicit.ReconProxy) == "" {
		if v := firstEnv(lookup, "RECON_PROXY"); v != "" {
			option.ReconProxy = v
		}
	}
	applyReconProviderEnvironment(option, lookup)
}

func applyRuntimeEnvironment(option *Option, explicit Option, lookup envLookup) {
	if strings.TrimSpace(explicit.DataDir) == "" {
		if v := firstEnv(lookup, "AISCAN_DATA_DIR"); v != "" {
			option.DataDir = v
		}
	}
	if strings.TrimSpace(explicit.RenderMode) == "" {
		option.RenderMode = firstEnv(lookup, "AISCAN_RENDER")
	}
	if strings.TrimSpace(explicit.REPLMode) == "" {
		option.REPLMode = firstEnv(lookup, "AISCAN_REPL")
	}
	if strings.TrimSpace(explicit.PlaywrightSession) == "" {
		option.PlaywrightSession = firstEnv(lookup, "PLAYWRIGHT_CLI_SESSION")
	}
}

var reconProviderEnvNames = []string{
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

func applyReconProviderEnvironment(option *Option, lookup envLookup) {
	for _, name := range reconProviderEnvNames {
		if value := firstEnv(lookup, name); value != "" {
			if option.ReconProviderKeys == nil {
				option.ReconProviderKeys = make(map[string]string)
			}
			option.ReconProviderKeys[name] = value
		}
	}
}

func selectedEnvProvider(option *Option, lookup envLookup, inferProvider func(string) string) string {
	if v := strings.ToLower(strings.TrimSpace(option.Provider)); v != "" {
		return normalizeProviderName(v)
	}
	if option.BaseURL != "" && inferProvider != nil {
		return inferProvider(option.BaseURL)
	}
	for _, providerName := range []string{"anthropic", "openai"} {
		if providerAPIKeyEnv(providerName, lookup) != "" {
			return providerName
		}
	}
	return ""
}

func providerBaseURLEnv(providerName string, lookup envLookup) string {
	providerName = canonicalEnvProvider(providerName)
	if providerName == "" {
		return ""
	}
	if providerName == "openai" {
		if v := firstEnv(lookup, "OPENAI_BASE_URL", "OPENAI_BASEURL", "OPENAI_API_BASE_URL", "OPENAI_API_BASE"); v != "" {
			return v
		}
	}
	return firstEnv(lookup, providerEnvName(providerName, "BASE_URL"), providerEnvName(providerName, "BASEURL"))
}

func providerModelEnv(providerName string, lookup envLookup) string {
	providerName = canonicalEnvProvider(providerName)
	if providerName == "" {
		return ""
	}
	return firstEnv(lookup, providerEnvName(providerName, "MODEL"))
}

func providerAPIKeyEnv(providerName string, lookup envLookup) string {
	providerName = canonicalEnvProvider(providerName)
	if providerName == "" {
		return ""
	}
	return firstEnv(lookup, providerEnvName(providerName, "API_KEY"))
}

func canonicalEnvProvider(providerName string) string {
	if strings.TrimSpace(providerName) == "" {
		return ""
	}
	return normalizeProviderName(providerName)
}

func normalizeProviderOptions(option *Option, inferProvider func(string) string) {
	if strings.TrimSpace(option.Provider) != "" {
		option.Provider = normalizeProviderName(option.Provider)
	} else if strings.TrimSpace(option.BaseURL) != "" && inferProvider != nil {
		option.Provider = normalizeProviderName(inferProvider(option.BaseURL))
	}
	for i := range option.Providers {
		providerName := strings.TrimSpace(option.Providers[i].Provider)
		if providerName != "" {
			option.Providers[i].Provider = normalizeProviderName(providerName)
		} else if inferProvider != nil {
			option.Providers[i].Provider = normalizeProviderName(inferProvider(option.Providers[i].BaseURL))
		}
	}
}

func normalizeProviderName(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "anthropic") {
		return "anthropic"
	}
	return "openai"
}

func providerEnvName(providerName, suffix string) string {
	providerName = strings.ToUpper(strings.TrimSpace(providerName))
	providerName = strings.ReplaceAll(providerName, "-", "_")
	return providerName + "_" + suffix
}

func firstEnv(lookup envLookup, names ...string) string {
	for _, name := range names {
		value, ok := lookup(name)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
