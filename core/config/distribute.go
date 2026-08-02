package config

import (
	"encoding/json"
	"fmt"
	"strings"

	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// LoadDistributeConfigYAML parses an aiscan.yaml file into the canonical proto
// representation. It bridges YAML's snake-case keys with the proto message.
func LoadDistributeConfigYAML(data []byte) (*types.DistributeConfig, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}
	jsonData, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("convert yaml to json: %w", err)
	}
	pb := new(types.DistributeConfig)
	if err := protojson.Unmarshal(jsonData, pb); err != nil {
		return nil, fmt.Errorf("unmarshal proto json: %w", err)
	}
	return pb, nil
}

// MarshalDistributeConfigYAML serializes the canonical proto config to YAML.
func MarshalDistributeConfigYAML(pb *types.DistributeConfig) ([]byte, error) {
	if pb == nil {
		return nil, nil
	}
	jsonData, err := protojson.Marshal(pb)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(jsonData, &raw); err != nil {
		return nil, err
	}
	return yaml.Marshal(raw)
}

// ActiveLLMProvider returns the selected LLM profile, or the first when the
// active id is missing/unknown, or nil when no profiles exist.
func ActiveLLMProvider(llm *types.LLMConfig) *types.LLMProviderConfig {
	if llm == nil || len(llm.Providers) == 0 {
		return nil
	}
	for _, provider := range llm.Providers {
		if provider.Id == llm.ActiveProfile {
			return NormalizeLLMProvider(provider)
		}
	}
	return NormalizeLLMProvider(llm.Providers[0])
}

// NormalizeLLMConfig canonicalizes the final profile-list representation. Old
// flat LLM configuration is intentionally not accepted.
func NormalizeLLMConfig(llm *types.LLMConfig) {
	if llm == nil {
		return
	}
	for index, provider := range llm.Providers {
		llm.Providers[index] = NormalizeLLMProvider(provider)
		provider = llm.Providers[index]
		if provider == nil {
			continue
		}
		if provider.Id == "" {
			provider.Id = fmt.Sprintf("profile-%d", index+1)
		}
		if provider.Name == "" {
			provider.Name = provider.Model
			if provider.Name == "" {
				provider.Name = provider.Provider
			}
		}
	}
	if active := ActiveLLMProvider(llm); active != nil {
		llm.ActiveProfile = active.Id
	}
}

// NormalizeLLMProvider trims and canonicalizes the provider protocol, inferring
// it from the base URL when blank.
func NormalizeLLMProvider(profile *types.LLMProviderConfig) *types.LLMProviderConfig {
	if profile == nil {
		return nil
	}
	profile.Provider = strings.ToLower(strings.TrimSpace(profile.Provider))
	if profile.Provider == "" {
		if strings.Contains(strings.ToLower(profile.BaseUrl), "anthropic.com") {
			profile.Provider = "anthropic"
		} else {
			profile.Provider = "openai"
		}
	}
	return profile
}
