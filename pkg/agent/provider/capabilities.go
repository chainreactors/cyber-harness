package provider

import (
	"net/url"
	"strings"
)

type StructuredOutputMode string

const (
	StructuredOutputJSONSchema StructuredOutputMode = "json_schema"
	StructuredOutputJSONObject StructuredOutputMode = "json_object"
)

// StructuredOutputModeFor resolves endpoint capabilities before a request is
// sent. Official DeepSeek currently supports JSON object mode but rejects the
// OpenAI json_schema response format.
func StructuredOutputModeFor(cfg ProviderConfig) StructuredOutputMode {
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err == nil && strings.EqualFold(parsed.Hostname(), "api.deepseek.com") {
		return StructuredOutputJSONObject
	}
	return StructuredOutputJSONSchema
}
