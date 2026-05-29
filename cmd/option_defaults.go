package cmd

import (
	"strconv"
	"strings"
)

func resolveString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func resolveSpace(space string) string {
	if space != "" && space != "default" {
		return space
	}
	if DefaultSpace != "" {
		return DefaultSpace
	}
	if space != "" {
		return space
	}
	return "default"
}

func applyDefaults(option *Option) {
	option.CyberhubURL = resolveString(option.CyberhubURL, DefaultCyberhubURL)
	option.CyberhubKey = resolveString(option.CyberhubKey, DefaultCyberhubKey)
	option.CyberhubMode = resolveString(resolveString(option.CyberhubMode, DefaultCyberhubMode), "merge")
	option.Proxy = resolveString(option.Proxy, DefaultScannerProxy)
	option.IOAURL = resolveString(option.IOAURL, DefaultIOAURL)
	option.IOANodeID = resolveString(option.IOANodeID, DefaultIOANodeID)
	option.IOANodeName = resolveString(option.IOANodeName, DefaultIOANodeName)
	option.Space = resolveSpace(option.Space)
	if option.Model == "" {
		option.Model = resolveString(DefaultModel, "deepseek-v4-pro")
	}
}
