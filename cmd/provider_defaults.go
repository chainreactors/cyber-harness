package cmd

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

	DefaultTavilyKeys      = "" // comma-separated Tavily API keys, injected at build time
	DefaultWebSearchProxy  = "" // HTTP proxy for web_search, injected at build time
)
