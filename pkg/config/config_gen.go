package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const configFileHeader = `# cyber-harness configuration; shared by Cyber-based applications.
# CLI > CYBER_* > current directory config > ~/.cyber/cyber.yaml > protocol env > defaults.
# Current directory: cyber.yaml, falling back to .cyber/cyber.yaml.
# Unspecified LLM values may come from OPENAI_* or ANTHROPIC_* environment variables.
# Example (uncomment and choose a model supported by your endpoint):
# llm:
#   provider: openai
#   base_url: https://api.example.com/v1
#   model: your-model
# Store personal credentials in user configuration or environment variables.
`

// InitialConfig generates a minimal document from explicit values only.
// It never reads files, environment variables, or host-specific defaults.
func InitialConfig(option *Option) ([]byte, error) {
	var input Option
	if option != nil {
		input = *option
	}
	option = &input
	if err := Validate(option); err != nil {
		return nil, err
	}
	doc := map[string]any{}
	llm := map[string]any{}
	for k, v := range map[string]string{"provider": option.Provider, "base_url": option.BaseURL, "model": option.Model, "api_key": option.APIKey, "proxy": option.LLMProxy} {
		if v != "" {
			llm[k] = v
		}
	}
	if option.MaxTokens != 0 {
		llm["max_tokens"] = option.MaxTokens
	}
	if option.ContextWindow != 0 {
		llm["context_window"] = option.ContextWindow
	}
	if option.ActiveProfile != "" {
		return nil, fmt.Errorf("init creates a minimal single-provider configuration; select existing profiles with config use")
	}
	if len(llm) > 0 {
		doc["llm"] = llm
	}
	if len(doc) == 0 {
		return []byte(configFileHeader), nil
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte(configFileHeader), data...), nil
}
func generateDefaultConfig() string { data, _ := InitialConfig(&Option{}); return string(data) }
