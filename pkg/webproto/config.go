package webproto

import "fmt"

// LLMProviderConfig is one named LLM profile. The profile selected by
// ActiveProfile is the runtime primary provider; the remaining entries are
// available for switching.
type LLMProviderConfig struct {
	ID       string `json:"id" yaml:"id,omitempty"`
	Name     string `json:"name" yaml:"name,omitempty"`
	Provider string `json:"provider" yaml:"provider"`
	BaseURL  string `json:"base_url" yaml:"base_url"`
	APIKey   string `json:"api_key,omitempty" yaml:"api_key"`
	Model    string `json:"model" yaml:"model"`
	Proxy    string `json:"proxy" yaml:"proxy"`
}

// LLMConfig is the provider profile list — the single representation of LLM
// settings. Selection is by ActiveProfile id, never by list position.
type LLMConfig struct {
	ActiveProfile string              `json:"active_profile,omitempty" yaml:"active_profile,omitempty"`
	Providers     []LLMProviderConfig `json:"providers,omitempty" yaml:"providers,omitempty"`
}

// Active returns the selected primary provider profile (Providers[0] when
// ActiveProfile is unset or unknown).
func (c LLMConfig) Active() LLMProviderConfig {
	if len(c.Providers) == 0 {
		return LLMProviderConfig{}
	}
	for _, p := range c.Providers {
		if p.ID == c.ActiveProfile {
			return p
		}
	}
	return c.Providers[0]
}

// MigrateLLMConfig normalizes a freshly loaded config exactly once: a legacy
// flat provider section becomes a single profile, missing ids/names are
// filled, and ActiveProfile is validated. It never writes back into flat.
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
	active := llm.Active()
	llm.ActiveProfile = active.ID
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
