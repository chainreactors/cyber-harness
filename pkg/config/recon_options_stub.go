//go:build !full

package config

type ReconOptions struct {
	FofaKey      string `long:"fofa-key" config:"fofa_key" hidden:"true"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" hidden:"true"`
	TavilyKey    string `long:"tavily-key" config:"tavily_key" hidden:"true"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" hidden:"true"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" hidden:"true"`
}
