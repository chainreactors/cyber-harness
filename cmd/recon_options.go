//go:build full

package cmd

type ReconOptions struct {
	FofaEmail    string `long:"fofa-email" config:"fofa_email" description:"FOFA account email for passive recon (or set env FOFA_EMAIL)"`
	FofaKey      string `long:"fofa-key" config:"fofa_key" description:"FOFA API key for passive recon (or set env FOFA_KEY)"`
	HunterToken  string `long:"hunter-token" config:"hunter_token" description:"Hunter web token (rarely needed; prefer hunter-api-key)"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" description:"Hunter API key (64-hex from console) (or env HUNTER_API_KEY)"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" description:"Outbound proxy for passive recon (socks5://host:port for hunter via mainland)"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" description:"Per-query asset limit for passive recon (0 = unlimited)"`
}
