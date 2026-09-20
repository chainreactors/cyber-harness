package config

func ResolveString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func ApplyDefaults(option *Option) {
	option.CyberhubURL = ResolveString(option.CyberhubURL, DefaultCyberhubURL)
	option.CyberhubKey = ResolveString(option.CyberhubKey, DefaultCyberhubKey)
	mode := ResolveString(option.CyberhubMode, DefaultCyberhubMode)
	option.CyberhubMode = ResolveString(mode, "merge")
	option.Proxy = ResolveString(option.Proxy, DefaultScannerProxy)
	option.NodeID = ResolveString(option.NodeID, DefaultNodeID)
	option.NodeName = ResolveString(option.NodeName, DefaultNodeName)
	if option.Model == "" {
		option.Model = DefaultModel
	}
}
