package config

import (
	"os"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
)

type envLookup func(string) (string, bool)

func ResolveRuntimeConfig(option *Option) (string, error) {
	explicit := *option
	configPath, err := LoadAndApplyConfig(option)
	if err != nil {
		return configPath, err
	}
	applyEnvironment(option, explicit, os.LookupEnv)
	ApplyDefaults(option)
	if strings.TrimSpace(option.DataDir) != "" {
		SetDataDir(option.DataDir)
	}
	return configPath, nil
}

func applyEnvironment(option *Option, explicit Option, lookup envLookup) {
	applyLLMEnvironment(option, explicit, lookup)
	applyScannerEnvironment(option, explicit, lookup)
	applyReconEnvironment(option, explicit, lookup)
}

func applyLLMEnvironment(option *Option, explicit Option, lookup envLookup) {
	providerExplicit := strings.TrimSpace(explicit.Provider) != ""
	if v := firstEnv(lookup, "AISCAN_PROVIDER", "AISCAN_LLM_PROVIDER"); v != "" && !providerExplicit {
		option.Provider = v
	}

	selectedProvider := selectedEnvProvider(option, lookup)
	if option.Provider == "" && selectedProvider != "" && !providerExplicit {
		option.Provider = selectedProvider
	}

	if strings.TrimSpace(explicit.BaseURL) == "" {
		if v := firstEnv(lookup, "AISCAN_BASE_URL", "AISCAN_BASEURL", "AISCAN_LLM_BASE_URL", "AISCAN_LLM_BASEURL"); v != "" {
			option.BaseURL = v
		} else if v := providerBaseURLEnv(selectedProvider, lookup); v != "" {
			option.BaseURL = v
		}
	}

	if strings.TrimSpace(explicit.Model) == "" {
		if v := firstEnv(lookup, "AISCAN_MODEL", "AISCAN_LLM_MODEL"); v != "" {
			option.Model = v
		} else if v := providerModelEnv(selectedProvider, lookup); v != "" {
			option.Model = v
		}
	}

	if strings.TrimSpace(explicit.APIKey) == "" {
		if v := providerAPIKeyEnv(selectedProvider, lookup); v != "" {
			option.APIKey = v
		} else if v := firstEnv(lookup, "AISCAN_API_KEY", "AISCAN_LLM_API_KEY"); v != "" {
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
		if v := firstEnv(lookup, "CYBERHUB_URL", "AISCAN_CYBERHUB_URL"); v != "" {
			option.CyberhubURL = v
		}
	}
	if strings.TrimSpace(explicit.CyberhubKey) == "" {
		if v := firstEnv(lookup, "CYBERHUB_KEY", "AISCAN_CYBERHUB_KEY"); v != "" {
			option.CyberhubKey = v
		}
	}
	if strings.TrimSpace(explicit.CyberhubMode) == "" {
		if v := firstEnv(lookup, "CYBERHUB_MODE", "AISCAN_CYBERHUB_MODE"); v != "" {
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
}

func selectedEnvProvider(option *Option, lookup envLookup) string {
	if v := strings.ToLower(strings.TrimSpace(option.Provider)); v != "" {
		return v
	}
	if option.BaseURL != "" {
		return agent.InferProviderFromBaseURL(option.BaseURL)
	}
	if firstEnv(lookup, "ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if firstEnv(lookup, "OPENAI_API_KEY") != "" {
		return "openai"
	}
	return ""
}

func providerBaseURLEnv(providerName string, lookup envLookup) string {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
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
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if providerName == "" {
		return ""
	}
	return firstEnv(lookup, providerEnvName(providerName, "MODEL"))
}

func providerAPIKeyEnv(providerName string, lookup envLookup) string {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	switch providerName {
	case "anthropic":
		return firstEnv(lookup, "ANTHROPIC_API_KEY")
	default:
		return firstEnv(lookup, "OPENAI_API_KEY")
	}
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
