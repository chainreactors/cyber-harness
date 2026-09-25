package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
)

type envLookup func(string) (string, bool)

// ResolveRuntimeConfig resolves parsed configuration with environment and defaults.
func ResolveRuntimeConfig(option *Option) (string, error) {
	return resolveRuntimeConfig(option, false)
}

// ResolveAgentRuntimeConfig selects the transport before resolving models so a
// node can enroll even when its local profile selection is stale or incomplete.
func ResolveAgentRuntimeConfig(option *Option) (string, error) {
	return resolveRuntimeConfig(option, true)
}

func resolveRuntimeConfig(option *Option, agentMode bool) (string, error) {
	if option.Sections == nil {
		option.Sections = NewSections()
	}
	explicit := explicitOptions(option)
	configPath, err := LoadAndApplyConfig(option)
	if err != nil {
		return configPath, err
	}
	if agentMode {
		transport, err := ResolveAgentTransport(option)
		if err != nil {
			return configPath, err
		}
		if transport == AgentTransportWeb {
			useRemoteLLM(option)
			useRemoteLLM(&explicit)
			option.Snapshot.Diagnostics = append(option.Snapshot.Diagnostics, "node mode: waiting for LLM configuration from the remote server")
		}
	}
	if err := finishRuntimeConfig(option, &explicit); err != nil {
		return configPath, err
	}
	return configPath, nil
}

func finishRuntimeConfig(option, explicit *Option) error {
	sections := option.Sections
	if sections == nil {
		sections = NewSections()
		option.Sections = sections
	}
	lookup := option.Context.defaults().LookupEnv
	if err := seedProviderProfile(option, explicit); err != nil {
		return err
	}
	var err error
	option.Resolved, err = sections.ResolveValues(option.Extensions, explicit.Extensions, lookup)
	if err != nil {
		return err
	}
	option.Extensions = option.Resolved.Values()
	applyEnvironment(option, *explicit, sourceLookup(option, lookup))
	if err := normalizeProviderOptions(option); err != nil {
		return err
	}
	ApplyDefaults(option)
	if _, err := ResolveOutputPolicy(option); err != nil {
		return err
	}
	option.DataDir = resolveDataDir(option.DataDir, option.Context)
	finishSnapshot(option, explicit)
	return nil
}

func applyEnvironment(option *Option, explicit Option, lookup envLookup) {
	if !option.remoteLLM {
		applyLLMEnvironment(option, explicit, lookup)
	}
	applyRuntimeEnvironment(option, explicit, lookup)
}

func applyLLMEnvironment(option *Option, explicit Option, lookup envLookup) {
	providerExplicit := explicit.hasExplicit("Provider")
	if v := firstEnv(lookup, "CYBER_PROVIDER"); v != "" && !providerExplicit {
		option.Provider = v
	}
	if option.Provider == "" && !providerExplicit {
		option.Provider = firstEnv(lookup, "LLM_PROVIDER")
	}

	// CYBER_BASE_URL is cyber's own namespace: an intentional override that wins
	// over a base URL set in the config file (CLI --base-url wins via the explicit gate).
	if !explicit.hasExplicit("BaseURL") {
		if v := firstEnv(lookup, "CYBER_BASE_URL"); v != "" {
			option.BaseURL = v
		} else if strings.TrimSpace(option.BaseURL) == "" && !explicit.hasExplicit("BaseURL") {
			option.BaseURL = firstEnv(lookup, "LLM_BASE_URL")
		}
	}

	selectedProvider := selectedEnvProvider(option, lookup)
	if option.Provider == "" && selectedProvider != "" && !providerExplicit {
		option.Provider = selectedProvider
	}
	// Provider-scoped base-URL envs (ANTHROPIC_BASE_URL, OPENAI_BASE_URL, …) are
	// commonly injected by the surrounding environment for *other* tools — e.g.
	// Claude-Code-style gateways export ANTHROPIC_BASE_URL. Treat them as a fallback
	// only: they must not silently override a base URL the user configured for cyber
	// (config file / Settings UI). Apply only when nothing else set one. Mirrors the
	// model handling below so a hub-launched agent that inherits the hub's env still
	// honors the Settings-saved base URL.
	if strings.TrimSpace(option.BaseURL) == "" && !explicit.hasExplicit("BaseURL") {
		if v := providerBaseURLEnv(selectedProvider, lookup); v != "" {
			option.BaseURL = v
		}
	}

	// CYBER_MODEL is cyber's own namespace: an intentional
	// override that still wins over a model set in the config file (CLI --model
	// wins over it via the explicit gate).
	if !explicit.hasExplicit("Model") {
		if v := firstEnv(lookup, "CYBER_MODEL"); v != "" {
			option.Model = v
		} else if strings.TrimSpace(option.Model) == "" && !explicit.hasExplicit("Model") {
			option.Model = firstEnv(lookup, "LLM_MODEL")
		}
	}
	// Provider-scoped model envs (ANTHROPIC_MODEL, OPENAI_MODEL, …) are commonly
	// injected by the surrounding environment for *other* tools — e.g. Claude-Code
	// style gateways export ANTHROPIC_MODEL. Treat them as a fallback only: they
	// must not silently override a model the user configured for cyber (config
	// file / Settings UI or --model). Apply only when nothing else set a model.
	if strings.TrimSpace(option.Model) == "" && !explicit.hasExplicit("Model") {
		if v := providerModelEnv(selectedProvider, lookup); v != "" {
			option.Model = v
		}
	}

	// CYBER_API_KEY is cyber's own namespace: an intentional override that wins
	// over a key set in the config file (CLI --api-key wins via the explicit gate).
	if !explicit.hasExplicit("APIKey") {
		if v := firstEnv(lookup, "CYBER_API_KEY"); v != "" {
			option.APIKey = v
		} else if strings.TrimSpace(option.APIKey) == "" && !explicit.hasExplicit("APIKey") {
			option.APIKey = firstEnv(lookup, "LLM_API_KEY")
		}
	}
	// Provider-scoped key envs (ANTHROPIC_API_KEY, OPENAI_API_KEY) are commonly
	// present for *other* tools. Treat them as a fallback only so they never override
	// a key the user configured for cyber (config file / Settings UI). Apply only
	// when nothing else set one — same rationale as base URL and model above.
	if strings.TrimSpace(option.APIKey) == "" && !explicit.hasExplicit("APIKey") {
		if v := providerAPIKeyEnv(selectedProvider, lookup); v != "" {
			option.APIKey = v
		}
	}

	if !explicit.hasExplicit("LLMProxy") {
		if v := firstEnv(lookup, "CYBER_LLM_PROXY"); v != "" {
			option.LLMProxy = v
		}
	}
}

func applyRuntimeEnvironment(option *Option, explicit Option, lookup envLookup) {
	if !explicit.hasExplicit("DataDir") {
		if v := firstEnv(lookup, "CYBER_DATA_DIR"); v != "" {
			option.DataDir = v
		}
	}
	if !explicit.hasExplicit("RenderMode") {
		option.RenderMode = firstEnv(lookup, "CYBER_RENDER")
	}
	if !explicit.hasExplicit("REPLMode") {
		option.REPLMode = firstEnv(lookup, "CYBER_REPL")
	}
	if !explicit.hasExplicit("PlaywrightSession") {
		option.PlaywrightSession = firstEnv(lookup, "PLAYWRIGHT_CLI_SESSION")
	}
}

func selectedEnvProvider(option *Option, lookup envLookup) string {
	if v := strings.ToLower(strings.TrimSpace(option.Provider)); v != "" {
		return provider.NormalizeProvider(v)
	}
	if option.BaseURL != "" {
		return provider.InferFromBaseURL(option.BaseURL)
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
	return firstEnv(lookup, providerEnvName(providerName, "BASE_URL"))
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
	providerName = provider.NormalizeProvider(providerName)
	if !provider.IsSupportedProvider(providerName) {
		return ""
	}
	return providerName
}

func normalizeProviderOptions(option *Option) error {
	if strings.TrimSpace(option.Provider) != "" || strings.TrimSpace(option.BaseURL) != "" {
		providerName, err := resolveProviderName(option.Provider, option.BaseURL)
		if err != nil {
			return err
		}
		option.Provider = providerName
	}
	for i := range option.Providers {
		providerName, err := resolveProviderName(option.Providers[i].Provider, option.Providers[i].BaseURL)
		if err != nil {
			return fmt.Errorf("LLM profile %q: %w", option.Providers[i].ID, err)
		}
		option.Providers[i].Provider = providerName
	}
	return nil
}

func resolveProviderName(name, baseURL string) (string, error) {
	name = provider.NormalizeProvider(name)
	if name == "" {
		name = provider.InferFromBaseURL(baseURL)
	}
	if !provider.IsSupportedProvider(name) {
		return "", fmt.Errorf("unsupported provider %q: use openai/anthropic or a known OpenAI-compatible vendor", name)
	}
	return name, nil
}

func providerEnvName(providerName, suffix string) string {
	providerName = strings.ToUpper(strings.TrimSpace(providerName))
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

// ResolveExecutionConfig resolves only extension and execution configuration.
// It deliberately does not inspect or normalize model-provider credentials.
func ResolveExecutionConfig(option *Option) (string, error) {
	if option.Sections == nil {
		option.Sections = NewSections()
	}
	explicit := explicitOptions(option)
	configPath, err := LoadAndApplyConfig(option)
	if err != nil {
		return configPath, err
	}
	option.Resolved, err = option.Sections.ResolveValues(option.Extensions, explicit.Extensions, os.LookupEnv)
	if err != nil {
		return configPath, err
	}
	option.Extensions = option.Resolved.Values()
	applyRuntimeEnvironment(option, explicit, os.LookupEnv)
	return configPath, nil
}
