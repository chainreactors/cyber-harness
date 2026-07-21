package webproto

import "fmt"

// LLMProviderConfig is one named LLM profile. The first profile is the
// runtime primary provider; the remaining entries are available for switching
// and are also compatible with core/config's existing fallback provider list.
type LLMProviderConfig struct {
	ID       string `json:"id" yaml:"id,omitempty"`
	Name     string `json:"name" yaml:"name,omitempty"`
	Provider string `json:"provider" yaml:"provider"`
	BaseURL  string `json:"base_url" yaml:"base_url"`
	APIKey   string `json:"api_key,omitempty" yaml:"api_key"`
	Model    string `json:"model" yaml:"model"`
	Proxy    string `json:"proxy" yaml:"proxy"`
}

type LLMConfig struct {
	Provider      string              `json:"provider" yaml:"provider"`
	BaseURL       string              `json:"base_url" yaml:"base_url"`
	APIKey        string              `json:"api_key,omitempty" yaml:"api_key"`
	Model         string              `json:"model" yaml:"model"`
	Proxy         string              `json:"proxy" yaml:"proxy"`
	ActiveProfile string              `json:"active_profile,omitempty" yaml:"active_profile,omitempty"`
	Providers     []LLMProviderConfig `json:"providers,omitempty" yaml:"providers,omitempty"`
}

// NormalizeLLMConfig ensures the selected profile is first (the runtime
// primary provider) and mirrors it into the legacy single-provider fields.
func NormalizeLLMConfig(llm *LLMConfig) {
	if len(llm.Providers) == 0 {
		if llm.Provider == "" && llm.BaseURL == "" && llm.Model == "" {
			return
		}
		id := llm.ActiveProfile
		if id == "" {
			id = "default"
		}
		llm.ActiveProfile = id
		llm.Providers = []LLMProviderConfig{{
			ID: id, Name: llm.Model, Provider: llm.Provider, BaseURL: llm.BaseURL,
			APIKey: llm.APIKey, Model: llm.Model, Proxy: llm.Proxy,
		}}
	}
	for i := range llm.Providers {
		if llm.Providers[i].ID == "" {
			llm.Providers[i].ID = fmt.Sprintf("profile-%d", i+1)
		}
		if llm.Providers[i].Name == "" {
			llm.Providers[i].Name = llm.Providers[i].Model
			if llm.Providers[i].Name == "" {
				llm.Providers[i].Name = llm.Providers[i].Provider
			}
		}
	}
	if llm.ActiveProfile == "" {
		llm.ActiveProfile = llm.Providers[0].ID
	}
	active := 0
	for i := range llm.Providers {
		if llm.Providers[i].ID == llm.ActiveProfile {
			active = i
			break
		}
	}
	if active > 0 {
		selected := llm.Providers[active]
		llm.Providers = append([]LLMProviderConfig{selected}, append(llm.Providers[:active], llm.Providers[active+1:]...)...)
	}
	primary := llm.Providers[0]
	llm.ActiveProfile = primary.ID
	llm.Provider = primary.Provider
	llm.BaseURL = primary.BaseURL
	llm.APIKey = primary.APIKey
	llm.Model = primary.Model
	llm.Proxy = primary.Proxy
}

// DistributeConfig is the configuration payload sent from the web server
// to agents. All secret fields are included so agents can use them.
// Also used by the settings UI (with secrets masked at the handler level).
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
