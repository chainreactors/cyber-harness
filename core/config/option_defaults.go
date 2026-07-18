package config

func ResolveString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func resolveSpace(space string) string {
	if space != "" && space != "default" {
		return space
	}
	return ResolveString(DefaultSpace, "default")
}

func ApplyDefaults(option *Option) {
	option.CyberhubURL = ResolveString(option.CyberhubURL, DefaultCyberhubURL)
	option.CyberhubKey = ResolveString(option.CyberhubKey, DefaultCyberhubKey)
	mode := ResolveString(option.CyberhubMode, DefaultCyberhubMode)
	option.CyberhubMode = ResolveString(mode, "merge")
	option.Proxy = ResolveString(option.Proxy, DefaultScannerProxy)
	option.IOAURL = ResolveString(option.IOAURL, DefaultIOAURL)
	option.IOANodeID = ResolveString(option.IOANodeID, DefaultIOANodeID)
	option.IOANodeName = ResolveString(option.IOANodeName, DefaultIOANodeName)
	option.Space = resolveSpace(option.Space)
	if option.Model == "" {
		option.Model = DefaultModel
	}
}
