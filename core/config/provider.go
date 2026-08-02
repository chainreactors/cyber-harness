package config

import "strings"

// Wire protocols aiscan speaks to LLM endpoints.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

var protocolBaseURLs = map[string]string{
	ProviderOpenAI:    "https://api.openai.com/v1",
	ProviderAnthropic: "https://api.anthropic.com/v1",
}

// vendorAliases maps well-known OpenAI-compatible vendors to their default
// endpoint. The provider field may name a wire protocol (openai, anthropic)
// or one of these vendors; the vendor name is preserved for status reporting,
// while the wire protocol goes through ProtocolOf.
var vendorAliases = map[string]string{
	"deepseek":    "https://api.deepseek.com/v1",
	"moonshot":    "https://api.moonshot.cn/v1",
	"kimi":        "https://api.moonshot.cn/v1",
	"zhipu":       "https://open.bigmodel.cn/api/paas/v4",
	"glm":         "https://open.bigmodel.cn/api/paas/v4",
	"qwen":        "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"dashscope":   "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"groq":        "https://api.groq.com/openai/v1",
	"xai":         "https://api.x.ai/v1",
	"grok":        "https://api.x.ai/v1",
	"mistral":     "https://api.mistral.ai/v1",
	"openrouter":  "https://openrouter.ai/api/v1",
	"together":    "https://api.together.xyz/v1",
	"siliconflow": "https://api.siliconflow.cn/v1",
	"ollama":      "http://localhost:11434/v1",
}

// NormalizeProvider lowercases and trims a provider name.
func NormalizeProvider(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ProtocolOf maps a provider name — a wire protocol or a known
// OpenAI-compatible vendor — to the protocol spoken on the wire. Unknown
// names yield "".
func ProtocolOf(name string) string {
	switch name = NormalizeProvider(name); name {
	case ProviderOpenAI, ProviderAnthropic:
		return name
	}
	if _, ok := vendorAliases[name]; ok {
		return ProviderOpenAI
	}
	return ""
}

// IsSupportedProvider reports whether name is a usable provider value: a wire
// protocol or a known vendor alias.
func IsSupportedProvider(name string) bool {
	return ProtocolOf(name) != ""
}

// ProviderBaseURL returns the endpoint for a provider name with no explicit
// base_url: the vendor alias endpoint, or the protocol's official one. "" for
// unknown names.
func ProviderBaseURL(name string) string {
	name = NormalizeProvider(name)
	if alias, ok := vendorAliases[name]; ok {
		return alias
	}
	return protocolBaseURLs[name]
}

// InferProviderFromBaseURL guesses the wire protocol from the base URL when no
// provider is set. The official Anthropic endpoint is unambiguous; everything
// else speaks the OpenAI protocol in the common case. A wrong guess is caught
// later as an actionable 404 from the provider, not a silent failure.
func InferProviderFromBaseURL(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "anthropic.com") {
		return ProviderAnthropic
	}
	return ProviderOpenAI
}
