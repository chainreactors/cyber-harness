package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
	webservice "github.com/chainreactors/cyber/pkg/web/service"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// ConfigCodec supplies the configuration surface owned by the host or product.
// The store handles layering, secret preservation and atomic file commits.
type ConfigCodec struct {
	Sections       *cfg.Sections
	ParseDocument  func(map[string]any) (*types.DistributeConfig, error)
	Document       func(*types.DistributeConfig) map[string]any
	Validate       func(*types.DistributeConfig) error
	PreserveValues func(cfg.Values, cfg.Values)
}

// SharedConfigCodec exposes shared settings and forwards opaque gateway
// configuration. Concrete service schemas belong to the execution nodes.
func SharedConfigCodec() ConfigCodec {
	sections := cfg.NewSections()
	services, err := gateway.Section()
	if err != nil {
		panic(err)
	}
	if _, err := sections.Add(services); err != nil {
		panic(err)
	}
	sections.Seal()
	return ConfigCodec{
		Sections: sections,
		ParseDocument: func(document map[string]any) (*types.DistributeConfig, error) {
			option := cfg.Option{Sections: sections}
			if err := cfg.LoadConfigDocument(document, &option); err != nil {
				return nil, err
			}
			value, err := cfg.SharedFromOption(&option)
			if err != nil {
				return nil, err
			}
			if document["llm"] == nil {
				value.Llm = nil
			}
			if fields, ok := document["agent"].(map[string]any); !ok {
				value.Agent = nil
			} else if _, present := fields["timeout"]; !present {
				value.Agent.Timeout = nil
			}
			if document["traffic"] == nil {
				value.Traffic = nil
			}
			if document["node"] == nil {
				value.Node = nil
			}
			return value, nil
		},
		Document: cfg.DistributeConfigDocument,
	}
}

// RuntimeLLM returns effective settings, including environment and CLI values.
// These values are distributed to nodes but never written into the file.
func (s *FileConfigStore) RuntimeLLM() provider.ProviderConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Runtime == nil {
		return provider.ProviderConfig{}
	}
	return cfg.ProviderConfig(s.Runtime)
}

type FileConfigStore struct {
	Explicit string
	// runtime supplies the original discovery context; its overrides are never saved.
	Runtime   *cfg.Option
	Overrides *cfg.Option
	Codec     ConfigCodec
	mu        sync.Mutex
}

func (s *FileConfigStore) configContext() *cfg.Context {
	if s.Runtime != nil {
		return s.Runtime.Context
	}
	return nil
}

func (s *FileConfigStore) stored() (*cfg.Snapshot, bool, *types.DistributeConfig, error) {
	snapshot, err := cfg.LoadSnapshot(s.configContext(), s.Explicit, s.Codec.Sections)
	if err != nil && errors.Is(err, os.ErrNotExist) && s.Explicit != "" {
		discovered, e := cfg.Discover(s.configContext(), s.Explicit)
		if e != nil {
			return nil, false, nil, e
		}
		environment := discovered.Context
		environment.Replacements = map[string][]byte{discovered.Target: []byte("{}")}
		snapshot, err = cfg.LoadSnapshot(&environment, s.Explicit, s.Codec.Sections)
	}
	if err != nil {
		return nil, false, nil, err
	}
	value, err := s.Codec.ParseDocument(cfg.CloneDocument(snapshot.Document))
	if err != nil {
		return nil, false, nil, err
	}
	fileOption, err := snapshot.FileOptions(s.Codec.Sections)
	if err != nil {
		return nil, false, nil, err
	}
	if len(fileOption.Providers) > 0 || cfg.HasSingleProviderFields(fileOption) {
		value.Llm = cfg.LLMFromOption(fileOption)
		cfg.NormalizeLLMConfig(value.Llm)
	}
	loaded := false
	for _, layer := range snapshot.Layers {
		if _, e := os.Stat(layer.Path); e == nil {
			loaded = true
		}
	}
	return snapshot, loaded, value, nil
}

func (s *FileConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, *types.DistributeConfig, error) {
	if err := ctx.Err(); err != nil {
		return "", false, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, loaded, value, err := s.stored()
	if err != nil {
		return "", false, nil, err
	}
	return snapshot.Target, loaded, value, nil
}

func (s *FileConfigStore) PrepareDistributeConfig(ctx context.Context, incoming *types.DistributeConfig) (*webservice.PreparedConfig, error) {
	var err error
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, _, current, err := s.stored()
	if err != nil {
		return nil, err
	}
	p := snapshot.Target
	incomingCopy := incoming
	if incomingCopy != nil {
		incoming = proto.Clone(incomingCopy).(*types.DistributeConfig)
	}
	if incoming == nil {
		incoming = &types.DistributeConfig{}
	}
	if incoming.Llm == nil {
		incoming.Llm = &types.LLMConfig{}
		if current.Llm != nil {
			incoming.Llm = proto.Clone(current.Llm).(*types.LLMConfig)
		}
	}
	cfg.NormalizeLLMConfig(incoming.Llm)
	if incoming.Agent == nil {
		incoming.Agent = current.Agent
	}
	if incoming.Traffic == nil {
		incoming.Traffic = current.Traffic
	}

	// Preserve existing secrets when incoming value is empty.
	if incoming.Node == nil {
		incoming.Node = current.GetNode()
	}
	PreserveLLMProfileSecrets(incoming.Llm, current.GetLlm())
	sections := s.Codec.Sections
	nextValues, currentValues := cfg.ValuesFromProto(incoming.Extensions), cfg.ValuesFromProto(current.Extensions)
	if s.Codec.PreserveValues != nil {
		s.Codec.PreserveValues(nextValues, currentValues)
	}
	incoming.Extensions, err = cfg.ValuesToProto(sections.Preserve(nextValues, currentValues))
	if err != nil {
		return nil, err
	}
	if s.Codec.Validate != nil {
		if err = s.Codec.Validate(incoming); err != nil {
			return nil, err
		}
	}

	before, after := s.Codec.Document(current), s.Codec.Document(incoming)
	target := snapshot.TargetDocument()
	// Convert a target's own shorthand when editing LLM settings. Never materialize inherited credentials.
	if !proto.Equal(current.GetLlm(), incoming.GetLlm()) {
		if raw, ok := target["llm"].(map[string]any); ok {
			if _, flat := raw["model"]; flat {
				own, parseErr := s.Codec.ParseDocument(target)
				if parseErr != nil {
					return nil, parseErr
				}
				ownDoc := s.Codec.Document(own)
				target["llm"] = ownDoc["llm"]
			}
		}
	}
	// The target may have been converted from flat LLM shorthand above.
	for i := range snapshot.Layers {
		if snapshot.Layers[i].Path == snapshot.Target {
			snapshot.Layers[i].Document = target
		}
	}
	patched, err := snapshot.ApplyChanges(before, after)
	if err != nil {
		return nil, err
	}
	candidate, err := snapshot.WithTargetDocument(patched, sections)
	if err != nil {
		return nil, err
	}
	merged, err := s.Codec.ParseDocument(cfg.CloneDocument(candidate.Document))
	if err != nil {
		return nil, err
	}
	fileOption, err := candidate.FileOptions(s.Codec.Sections)
	if err != nil {
		return nil, err
	}
	if len(fileOption.Providers) > 0 || cfg.HasSingleProviderFields(fileOption) {
		merged.Llm = cfg.LLMFromOption(fileOption)
		cfg.NormalizeLLMConfig(merged.Llm)
	}
	runtimeOption := cfg.Option{Sections: s.Codec.Sections, Context: s.configContext()}
	if s.Overrides != nil {
		runtimeOption = *s.Overrides
		runtimeOption.Sections, runtimeOption.Context = s.Codec.Sections, s.configContext()
	}
	runtimeOption.ConfigFile = s.Explicit
	if err := cfg.ResolveRuntimeSnapshot(&runtimeOption, candidate); err != nil {
		return nil, err
	}
	next, err := yaml.Marshal(patched)
	if err != nil {
		return nil, err
	}
	tmpPath, err := cfg.PrepareFile(p, next)
	if err != nil {
		return nil, err
	}
	return &webservice.PreparedConfig{Config: merged, Runtime: &runtimeOption, RuntimePath: tmpPath, TargetPath: p}, nil
}

func (s *FileConfigStore) CommitDistributeConfig(ctx context.Context, prepared *webservice.PreparedConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prepared == nil || prepared.RuntimePath == "" || prepared.TargetPath == "" {
		return fmt.Errorf("prepared config is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := cfg.CommitFile(prepared.RuntimePath, prepared.TargetPath); err != nil {
		return err
	}
	prepared.RuntimePath = ""
	if prepared.Runtime != nil {
		s.Runtime = prepared.Runtime
	}
	return nil
}

func (s *FileConfigStore) DiscardDistributeConfig(prepared *webservice.PreparedConfig) {
	if prepared == nil || prepared.RuntimePath == "" {
		return
	}
	_ = os.Remove(prepared.RuntimePath)
	prepared.RuntimePath = ""
}

func PreserveLLMProfileSecrets(incoming *types.LLMConfig, existing *types.LLMConfig) {
	if incoming == nil {
		return
	}
	byID := make(map[string]*types.LLMProviderConfig)
	if existing != nil {
		for _, profile := range existing.Providers {
			if profile.Id != "" {
				byID[profile.Id] = profile
			}
		}
	}
	var existingProviders []*types.LLMProviderConfig
	if existing != nil {
		existingProviders = existing.Providers
	}
	for i, profile := range incoming.Providers {
		if profile == nil || strings.TrimSpace(profile.ApiKey) != "" {
			continue
		}
		if current, ok := byID[profile.Id]; ok {
			profile.ApiKey = current.ApiKey
			continue
		}
		if profile.Id == "" && i < len(existingProviders) && existingProviders[i].GetId() == "" {
			profile.ApiKey = existingProviders[i].GetApiKey()
		}
	}
}
