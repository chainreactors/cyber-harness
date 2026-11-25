package config

import (
	"encoding/json"
	"fmt"
	"strings"

	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// LoadDistributeConfigYAML parses an cyber.yaml file into the canonical proto
// representation. It bridges YAML's snake-case keys with the proto message.
func LoadDistributeConfigYAML(data []byte) (*types.DistributeConfig, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}
	return LoadDistributeConfigDocument(raw)
}

// LoadDistributeConfigDocument maps an already decoded cyber.yaml document onto
// the canonical proto representation. Applications whose configuration is wider
// than the proto drop their own keys before calling this.
func LoadDistributeConfigDocument(raw map[string]any) (*types.DistributeConfig, error) {
	document := CloneDocument(raw)
	extensions := map[string]any{}
	for key, value := range document {
		switch key {
		case "llm", "agent", "node", "traffic":
		case "extensions":
			return nil, fmt.Errorf("unknown configuration field extensions")
		default:
			extensions[key] = value
			delete(document, key)
		}
	}
	document["extensions"] = extensions
	jsonData, err := json.Marshal(document)
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
// Proto field names are used so the result stays readable by the flags-backed
// loader, which matches on the Option schema's snake-case keys.
func MarshalDistributeConfigYAML(pb *types.DistributeConfig) ([]byte, error) {
	if pb == nil {
		return nil, nil
	}
	return yaml.Marshal(DistributeConfigDocument(pb))
}

// DistributeConfigDocument projects the transport config directly into the
// document consumed by the existing option decoder and file editor.
func DistributeConfigDocument(pb *types.DistributeConfig) map[string]any {
	document := map[string]any{}
	if pb == nil {
		return document
	}
	for key, value := range pb.Extensions {
		if value == nil {
			document[key] = nil
		} else {
			document[key] = value.AsMap()
		}
	}
	if llm := pb.Llm; llm != nil {
		providers := make([]any, 0, len(llm.Providers))
		for _, p := range llm.Providers {
			fields := map[string]any{
				"id": p.GetId(), "name": p.GetName(), "provider": p.GetProvider(),
				"base_url": p.GetBaseUrl(), "api_key": p.GetApiKey(), "model": p.GetModel(),
				"proxy": p.GetProxy(), "max_tokens": int(p.GetMaxTokens()),
				"context_window": int(p.GetContextWindow()), "timeout": int(p.GetTimeout()),
			}
			if p != nil && p.Images != nil {
				fields["images"] = *p.Images
			}
			providers = append(providers, fields)
		}
		document["llm"] = map[string]any{"active_profile": llm.ActiveProfile, "providers": providers}
	}
	if agent := pb.Agent; agent != nil {
		fields := map[string]any{
			"tools": append([]string{}, agent.Tools...), "heartbeat": int(agent.Heartbeat),
			"eval_criteria": agent.EvalCriteria, "eval_model": agent.EvalModel,
			"eval_rounds": agent.EvalRounds, "capture_provider_frames": agent.CaptureProviderFrames,
		}
		if agent.Timeout != nil {
			fields["timeout"] = int(*agent.Timeout)
		}
		document["agent"] = fields
	}
	if node := pb.Node; node != nil {
		document["node"] = map[string]any{"id": node.Id, "name": node.Name}
	}
	if traffic := pb.Traffic; traffic != nil {
		document["traffic"] = map[string]any{
			"body_storage": traffic.BodyStorage, "body_max_bytes": traffic.BodyMaxBytes,
			"body_retention_bytes": traffic.BodyRetentionBytes,
		}
	}
	return document
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
	if llm.ActiveProfile != "" {
		return nil
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
	if active := ActiveLLMProvider(llm); active != nil && llm.ActiveProfile == "" {
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
