package config

import "reflect"

func ResolveString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func ApplyDefaults(option *Option) {
	defaults := map[string]string{
		"Transport": "auto", "OutputFormat": "text", "ViewFormat": "terminal",
		"CyberhubURL": DefaultCyberhubURL, "CyberhubKey": DefaultCyberhubKey,
		"CyberhubMode": ResolveString(DefaultCyberhubMode, "merge"), "Proxy": DefaultScannerProxy,
		"NodeID": DefaultNodeID, "NodeName": DefaultNodeName, "Model": DefaultModel,
	}
	visitOptions(option, func(field reflect.StructField, value reflect.Value, path string) {
		if option.present[path] || option.fieldExplicit(field, value) || !value.IsZero() {
			return
		}

		if field.Name == "Timeout" {
			value.SetInt(3600)
		}
		if fallback, ok := defaults[field.Name]; ok {
			value.SetString(fallback)
		}
	})
}
