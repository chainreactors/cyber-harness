package webproto

// DistributeConfig is the configuration payload sent from the web server
// to agents. All secret fields are included so agents can use them.
// Also used by the settings UI (with secrets masked at the handler level).
type DistributeConfig struct {
	LLM struct {
		Provider string `json:"provider" yaml:"provider"`
		BaseURL  string `json:"base_url" yaml:"base_url"`
		APIKey   string `json:"api_key,omitempty" yaml:"api_key"`
		Model    string `json:"model" yaml:"model"`
		Proxy    string `json:"proxy" yaml:"proxy"`
	} `json:"llm" yaml:"llm"`
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
		Verify        string `json:"verify" yaml:"verify"`
		VerifyTimeout int    `json:"verify_timeout" yaml:"verify_timeout"`
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
