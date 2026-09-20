package api

import (
	"context"
	"fmt"
	"strings"

	agentprovider "github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
	configpkg "github.com/chainreactors/cyber/pkg/config"
)

// ConfigBackend owns configuration updates and runtime publication. The API
// uses this business interface and owns no profiles or staged resources.
type ConfigBackend interface {
	GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error)
	SaveConfig(context.Context, *types.DistributeConfig) (*types.ConfigView, error)
	ActivateConfig(context.Context, string) (*types.ConfigView, error)
}

type ConfigOptions struct {
	Sections *configpkg.Sections
	Project  func(*types.DistributeConfig, *types.ConfigView)
}
type Config struct {
	backend ConfigBackend
	options ConfigOptions
}

func NewConfig(backend ConfigBackend, options ConfigOptions) *Config {
	if options.Sections != nil {
		options.Sections.Seal()
	}
	return &Config{backend: backend, options: options}
}

func (c *Config) GetConfig(ctx context.Context, _ *types.GetConfigRequest) (*types.GetConfigResponse, error) {
	view, err := c.View(ctx)
	if err != nil {
		return nil, err
	}
	return &types.GetConfigResponse{Config: view}, nil
}

func (c *Config) UpdateConfig(ctx context.Context, request *types.UpdateConfigRequest) (*types.UpdateConfigResponse, error) {
	if request == nil || request.GetConfig() == nil {
		return nil, Errorf(CodeInvalidArgument, "config is required")
	}
	if c == nil || c.backend == nil {
		return nil, Errorf(CodeFailedPrecondition, "config service is not configured")
	}
	view, err := c.backend.SaveConfig(ctx, request.Config)
	if err != nil {
		return nil, err
	}
	return &types.UpdateConfigResponse{Config: view}, nil
}

func (c *Config) ActivateProfile(ctx context.Context, request *types.ActivateProfileRequest) (*types.ActivateProfileResponse, error) {
	if request == nil {
		return nil, Errorf(CodeInvalidArgument, "request is required")
	}
	if c == nil || c.backend == nil {
		return nil, Errorf(CodeFailedPrecondition, "config service is not configured")
	}
	view, err := c.backend.ActivateConfig(ctx, request.ProfileId)
	if err != nil {
		return nil, err
	}
	return &types.ActivateProfileResponse{Config: view}, nil
}

func (c *Config) TestLLM(ctx context.Context, request *types.LLMProbeRequest) (*types.LLMProbeResult, error) {
	result, err := agentprovider.TestLLM(ctx, request, c.storedLLMAPIKey(ctx, request.GetProfileId()))
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return result, nil
}

func (c *Config) ListModels(ctx context.Context, request *types.LLMProbeRequest) (*types.ListModelsResult, error) {
	result, err := agentprovider.ListLLMModels(ctx, request, c.storedLLMAPIKey(ctx, request.GetProfileId()))
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return result, nil
}

func (c *Config) TestConnection(ctx context.Context, request *types.TestConnectionRequest) (*types.TestConnectionResponse, error) {
	if request == nil {
		return nil, Errorf(CodeInvalidArgument, "request is required")
	}
	stored, _ := c.Distribute(ctx)
	checks, err := c.options.Sections.TestConnection(ctx, request.GetSection(), request.GetConfig(), stored)
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return &types.TestConnectionResponse{Checks: checks}, nil
}

func (c *Config) View(ctx context.Context) (*types.ConfigView, error) {
	if c == nil || c.backend == nil {
		return nil, Errorf(CodeFailedPrecondition, "config store is not configured")
	}
	path, loaded, config, err := c.backend.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	view := ConfigView(config, path, loaded)
	if c.options.Sections != nil {
		view.Extensions = c.options.Sections.ProtoViews(config.GetExtensions())
	}
	if c.options.Project != nil {
		c.options.Project(config, view)
	}
	return view, nil
}

func (c *Config) Distribute(ctx context.Context) (*types.DistributeConfig, error) {
	if c == nil || c.backend == nil {
		return nil, Errorf(CodeFailedPrecondition, "config store is not configured")
	}
	_, _, config, err := c.backend.GetDistributeConfig(ctx)
	return config, err
}

func (c *Config) storedLLMAPIKey(ctx context.Context, profileID string) string {
	config, err := c.Distribute(ctx)
	if err != nil {
		return ""
	}
	profileID = strings.TrimSpace(profileID)
	if profileID != "" {
		for _, profile := range config.GetLlm().GetProviders() {
			if profile.GetId() == profileID {
				return strings.TrimSpace(profile.GetApiKey())
			}
		}
		return ""
	}
	if active := configpkg.ActiveLLMProvider(config.GetLlm()); active != nil {
		return strings.TrimSpace(active.GetApiKey())
	}
	return ""
}

func ValidateLLMConfig(config *types.LLMConfig) error {
	if config == nil {
		return nil
	}
	for index, profile := range config.Providers {
		profile = configpkg.NormalizeLLMProvider(profile)
		if profile == nil {
			return fmt.Errorf("LLM profile #%d is empty", index+1)
		}
		if !agentprovider.IsSupportedProvider(profile.Provider) {
			return fmt.Errorf("LLM provider %q is unsupported: use openai/anthropic or a known OpenAI-compatible vendor", profile.Provider)
		}
		if strings.TrimSpace(profile.Model) == "" {
			name := strings.TrimSpace(profile.Name)
			if name == "" {
				name = strings.TrimSpace(profile.Id)
			}
			if name == "" {
				name = fmt.Sprintf("#%d", index+1)
			}
			return fmt.Errorf("LLM profile %q model is required", name)
		}
		if profile.MaxTokens < 0 {
			return fmt.Errorf("LLM max_tokens must be zero or positive")
		}
		if profile.ContextWindow < 0 {
			return fmt.Errorf("LLM context_window must be zero or positive")
		}
		if profile.Timeout < 0 {
			return fmt.Errorf("LLM timeout must be zero or positive")
		}
	}
	return nil
}

func ConfigView(config *types.DistributeConfig, path string, loaded bool) *types.ConfigView {
	view := &types.ConfigView{Path: path, Loaded: loaded}
	if config == nil {
		return view
	}
	view.Llm = &types.LLMView{ActiveProfile: config.GetLlm().GetActiveProfile()}
	for _, raw := range config.GetLlm().GetProviders() {
		profile := configpkg.NormalizeLLMProvider(raw)
		if profile == nil {
			continue
		}
		item := &types.LLMProviderView{
			Id: profile.Id, Name: profile.Name, Provider: profile.Provider,
			BaseUrl: profile.BaseUrl, ApiKeyConfigured: profile.ApiKey != "",
			Model: profile.Model, Proxy: profile.Proxy, MaxTokens: profile.MaxTokens,
			ContextWindow: profile.ContextWindow, Timeout: profile.Timeout, Images: profile.Images,
		}
		view.Llm.Providers = append(view.Llm.Providers, item)
		if profile.Id == view.Llm.ActiveProfile {
			view.Llm.Active = item
		}
	}
	if view.Llm.Active == nil && len(view.Llm.Providers) > 0 {
		view.Llm.Active = view.Llm.Providers[0]
		view.Llm.ActiveProfile = view.Llm.Active.Id
	}
	view.Cyberhub = &types.CyberhubView{Url: config.GetCyberhub().GetUrl(), KeyConfigured: config.GetCyberhub().GetKey() != "", Mode: config.GetCyberhub().GetMode(), Proxy: config.GetCyberhub().GetProxy()}
	view.Recon = &types.ReconView{FofaKeyConfigured: config.GetRecon().GetFofaKey() != "", HunterApiKeyConfigured: config.GetRecon().GetHunterApiKey() != "", Proxy: config.GetRecon().GetProxy(), Limit: config.GetRecon().GetLimit()}
	view.Scan = &types.ScanConfig{Verify: config.GetScan().GetVerify()}
	view.Search = &types.SearchView{TavilyKeysConfigured: config.GetSearch().GetTavilyKeys() != ""}
	view.Agent = &types.AgentConfig{Tools: append([]string(nil), config.GetAgent().GetTools()...), Timeout: config.GetAgent().GetTimeout()}
	return view
}
