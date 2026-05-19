package cmd

import "github.com/chainreactors/aiscan/pkg/provider"

var (
	DefaultProvider = "deepseek"
	DefaultBaseURL  = ""
	DefaultAPIKey   = ""
	DefaultModel    = "deepseek-v4-pro"

	DefaultScannerProxy = ""

	DefaultCyberhubURL  = ""
	DefaultCyberhubKey  = ""
	DefaultCyberhubMode = "merge"

	DefaultVerify        = "auto"
	DefaultVerifyTimeout = ""

	DefaultIOAURL      = ""
	DefaultIOANodeID   = ""
	DefaultIOANodeName = ""
	DefaultSpace       = ""
)

func defaultProviderConfig() provider.ProviderConfig {
	return provider.ProviderConfig{
		Provider: DefaultProvider,
		BaseURL:  DefaultBaseURL,
		APIKey:   DefaultAPIKey,
		Model:    DefaultModel,
	}
}
