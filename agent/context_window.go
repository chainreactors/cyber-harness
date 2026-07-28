package agent

import "strings"

// DefaultContextWindow is used when a model-specific context size is unknown.
const DefaultContextWindow = 128000

// modelContextWindows maps model name prefixes to their context window sizes.
// Entries are checked in order; the first prefix match wins.
var modelContextWindows = []struct {
	prefix string
	tokens int
}{
	{"claude-opus-4-8", 1000000},
	{"claude-opus-4-7", 1000000},
	{"claude-opus-4-6", 1000000},
	{"claude-sonnet-4-6", 1000000},
	{"claude-fable-5", 1000000},
	{"claude-opus-4", 200000},
	{"claude-sonnet-4", 200000},
	{"claude-haiku-4", 200000},
	{"claude-3", 200000},

	{"deepseek-v4", 1000000},
	{"deepseek-r1", 163840},
	{"deepseek-v3", 163840},
	{"deepseek-chat", 128000},
	{"deepseek-reasoner", 128000},
	{"deepseek", 128000},

	{"gpt-5.4", 1050000},
	{"gpt-5.5", 1050000},
	{"gpt-5", 400000},
	{"gpt-4.1", 1047576},
	{"gpt-4o", 128000},
	{"gpt-4-turbo", 128000},
	{"gpt-4-1", 128000},
	{"gpt-4-0", 8192},
	{"gpt-4", 8192},
	{"o4-mini", 200000},
	{"o3", 200000},
	{"o1", 200000},

	{"gemini", 1048576},

	{"qwen3.7", 1000000},
	{"qwen3.6", 1000000},
	{"qwen3-coder", 262144},
	{"qwen3", 262144},
	{"qwen", 128000},

	{"kimi", 262144},
	{"moonshot", 262144},
}

// ModelContextWindow returns the known context window for model.
func ModelContextWindow(model string) int {
	model = strings.ToLower(model)
	for _, entry := range modelContextWindows {
		if strings.HasPrefix(model, entry.prefix) {
			return entry.tokens
		}
	}
	return DefaultContextWindow
}
