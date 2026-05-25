//go:build !full

package cmd

type ReconOptions struct {
	FofaEmail    string `long:"fofa-email" config:"fofa_email" hidden:"true"`
	FofaKey      string `long:"fofa-key" config:"fofa_key" hidden:"true"`
	HunterToken  string `long:"hunter-token" config:"hunter_token" hidden:"true"`
	HunterAPIKey string `long:"hunter-api-key" config:"hunter_api_key" hidden:"true"`
	ReconProxy   string `long:"recon-proxy" config:"proxy" hidden:"true"`
	ReconLimit   *int   `long:"recon-limit" config:"limit" hidden:"true"`
}
