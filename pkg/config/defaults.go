package config

// Link-time defaults retain their names for -ldflags -X. Configure them before
// starting hosts through SetProviderDefaults and SetNodeDefaults.
var (
	DefaultProvider = "openai"
	DefaultBaseURL  = ""
	DefaultAPIKey   = ""
	DefaultModel    = ""

	DefaultNodeID   = ""
	DefaultNodeName = ""
)

// SetProviderDefaults replaces the link-time provider defaults and returns a
// restore function. Call only during startup, before configuration is resolved.
// Only Provider, BaseURL, APIKey and Model participate in these defaults.
func SetProviderDefaults(value LLMOptions) func() {
	provider, baseURL, apiKey, model := DefaultProvider, DefaultBaseURL, DefaultAPIKey, DefaultModel
	DefaultProvider, DefaultBaseURL, DefaultAPIKey, DefaultModel = value.Provider, value.BaseURL, value.APIKey, value.Model
	return func() {
		DefaultProvider, DefaultBaseURL, DefaultAPIKey, DefaultModel = provider, baseURL, apiKey, model
	}
}

// SetNodeDefaults replaces the link-time node identity defaults and returns a
// restore function. Call only during startup, before configuration is resolved.
func SetNodeDefaults(id, name string) func() {
	oldID, oldName := DefaultNodeID, DefaultNodeName
	DefaultNodeID, DefaultNodeName = id, name
	return func() { DefaultNodeID, DefaultNodeName = oldID, oldName }
}
