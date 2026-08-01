package config

import (
	"fmt"
	"strings"
)

// LLMProviderConfig is one named LLM profile distributed by the Web server.
type LLMProviderConfig struct {
	ID            string `json:"id" yaml:"id,omitempty"`
	Name          string `json:"name" yaml:"name,omitempty"`
	Provider      string `json:"provider" yaml:"provider"`
	BaseURL       string `json:"base_url" yaml:"base_url"`
	APIKey        string `json:"api_key,omitempty" yaml:"api_key"`
	Model         string `json:"model" yaml:"model"`
	Proxy         string `json:"proxy" yaml:"proxy"`
	MaxTokens     int    `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	ContextWindow int    `json:"context_window,omitempty" yaml:"context_window,omitempty"`
}

type LLMConfig struct {
	ActiveProfile string              `json:"active_profile,omitempty" yaml:"active_profile,omitempty"`
	Providers     []LLMProviderConfig `json:"providers,omitempty" yaml:"providers,omitempty"`
}

func (c LLMConfig) Active() LLMProviderConfig {
	if len(c.Providers) == 0 {
		return LLMProviderConfig{}
	}
	for _, provider := range c.Providers {
		if provider.ID == c.ActiveProfile {
			return NormalizeLLMProvider(provider)
		}
	}
	return NormalizeLLMProvider(c.Providers[0])
}

func MigrateLLMConfig(llm *LLMConfig, flat LLMProviderConfig) {
	if len(llm.Providers) == 0 {
		if flat.Provider == "" && flat.BaseURL == "" && flat.Model == "" {
			return
		}
		flat.ID = llm.ActiveProfile
		if flat.ID == "" {
			flat.ID = "default"
		}
		llm.Providers = []LLMProviderConfig{flat}
	}
	for index := range llm.Providers {
		llm.Providers[index] = NormalizeLLMProvider(llm.Providers[index])
		if llm.Providers[index].ID == "" {
			llm.Providers[index].ID = fmt.Sprintf("profile-%d", index+1)
		}
		if llm.Providers[index].Name == "" {
			llm.Providers[index].Name = llm.Providers[index].Model
			if llm.Providers[index].Name == "" {
				llm.Providers[index].Name = llm.Providers[index].Provider
			}
		}
	}
	llm.ActiveProfile = llm.Active().ID
}

func NormalizeLLMProvider(profile LLMProviderConfig) LLMProviderConfig {
	profile.Provider = strings.ToLower(strings.TrimSpace(profile.Provider))
	if profile.Provider == "" {
		if strings.Contains(strings.ToLower(profile.BaseURL), "anthropic.com") {
			profile.Provider = "anthropic"
		} else {
			profile.Provider = "openai"
		}
	}
	return profile
}

// DistributeConfig is the shared configuration document loaded by the Web
// server and consumed by remote agents. HTTP masking stays in pkg/web.
type DistributeConfig struct {
	LLM      LLMConfig `json:"llm" yaml:"llm"`
	Cyberhub struct {
		URL   string `json:"url" yaml:"url"`
		Key   string `json:"key,omitempty" yaml:"key"`
		Mode  string `json:"mode" yaml:"mode"`
		Proxy string `json:"proxy" yaml:"proxy"`
	} `json:"cyberhub" yaml:"cyberhub"`
	Recon struct {
		FofaEmail    string `json:"fofa_email" yaml:"fofa_email"`
		FofaKey      string `json:"fofa_key,omitempty" yaml:"fofa_key"`
		HunterToken  string `json:"hunter_token,omitempty" yaml:"hunter_token"`
		HunterAPIKey string `json:"hunter_api_key,omitempty" yaml:"hunter_api_key"`
		Proxy        string `json:"proxy" yaml:"proxy"`
		Limit        *int   `json:"limit,omitempty" yaml:"limit,omitempty"`
	} `json:"recon" yaml:"recon"`
	Scan struct {
		Verify string `json:"verify" yaml:"verify"`
	} `json:"scan" yaml:"scan"`
	Search struct {
		TavilyKeys string `json:"tavily_keys,omitempty" yaml:"tavily_keys"`
	} `json:"search" yaml:"search"`
	IOA struct {
		URL      string `json:"url" yaml:"url"`
		Token    string `json:"token,omitempty" yaml:"token"`
		NodeName string `json:"node_name" yaml:"node_name"`
		Space    string `json:"space" yaml:"space"`
	} `json:"ioa" yaml:"ioa"`
	Agent struct {
		Tools       []string `json:"tools,omitempty" yaml:"tools,omitempty"`
		Timeout     int      `json:"timeout" yaml:"timeout"`
		SaveSession bool     `json:"save_session" yaml:"save_session"`
	} `json:"agent" yaml:"agent"`
}
