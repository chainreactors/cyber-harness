package api

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentprobe "github.com/chainreactors/aiscan/agent/probe"
	agentprovider "github.com/chainreactors/aiscan/agent/provider"
	configpkg "github.com/chainreactors/aiscan/core/config"
	probe "github.com/chainreactors/aiscan/pkg/probe"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
)

type ConfigStore interface {
	GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error)
	PrepareDistributeConfig(context.Context, *types.DistributeConfig) (*PreparedConfig, error)
	CommitDistributeConfig(context.Context, *PreparedConfig) error
	DiscardDistributeConfig(*PreparedConfig)
}

type PreparedConfig struct {
	Config      *types.DistributeConfig
	RuntimePath string
	TargetPath  string
}

type ConfigOptions struct {
	Store     ConfigStore
	Build     func(context.Context, *PreparedConfig) (*runner.App, error)
	Apply     func(*runner.App)
	Broadcast func(*types.DistributeConfig)
}

type Config struct {
	mu        sync.Mutex
	store     ConfigStore
	build     func(context.Context, *PreparedConfig) (*runner.App, error)
	apply     func(*runner.App)
	broadcast func(*types.DistributeConfig)
}

func NewConfig(options ConfigOptions) *Config {
	return &Config{store: options.Store, build: options.Build, apply: options.Apply, broadcast: options.Broadcast}
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
	view, err := c.Save(ctx, request.Config)
	if err != nil {
		return nil, err
	}
	return &types.UpdateConfigResponse{Config: view}, nil
}

func (c *Config) ActivateProfile(ctx context.Context, request *types.ActivateProfileRequest) (*types.ActivateProfileResponse, error) {
	if request == nil {
		return nil, Errorf(CodeInvalidArgument, "request is required")
	}
	view, err := c.Activate(ctx, request.ProfileId)
	if err != nil {
		return nil, err
	}
	return &types.ActivateProfileResponse{Config: view}, nil
}

func (c *Config) TestLLM(ctx context.Context, request *types.LLMProbeRequest) (*types.LLMProbeResult, error) {
	result, err := agentprobe.TestLLM(ctx, request, c.storedLLMAPIKey(ctx, request.GetProfileId()))
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return result, nil
}

func (c *Config) ListModels(ctx context.Context, request *types.LLMProbeRequest) (*types.ListModelsResult, error) {
	result, err := agentprobe.ListLLMModels(ctx, request, c.storedLLMAPIKey(ctx, request.GetProfileId()))
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
	checks, err := probe.TestConn(ctx, request.GetSection(), request.GetConfig(), stored)
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return &types.TestConnectionResponse{Checks: checks}, nil
}

func (c *Config) View(ctx context.Context) (*types.ConfigView, error) {
	if c == nil || c.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "config store is not configured")
	}
	path, loaded, config, err := c.store.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return ConfigView(config, path, loaded), nil
}

func (c *Config) Save(ctx context.Context, config *types.DistributeConfig) (*types.ConfigView, error) {
	if c == nil || c.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "config store is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ValidateLLMConfig(config.GetLlm()); err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	prepared, err := c.store.PrepareDistributeConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			c.store.DiscardDistributeConfig(prepared)
		}
	}()
	if prepared == nil || prepared.Config == nil {
		return nil, fmt.Errorf("config store returned no prepared config")
	}
	if err := ValidateLLMConfig(prepared.Config.GetLlm()); err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	var next *runner.App
	if c.build != nil {
		next, err = c.build(ctx, prepared)
		if err != nil {
			return nil, NewError(CodeFailedPrecondition, fmt.Errorf("reload aiscan runtime: %w", err))
		}
		if next == nil {
			return nil, fmt.Errorf("reload aiscan runtime returned no app")
		}
	}
	if err := c.store.CommitDistributeConfig(ctx, prepared); err != nil {
		if next != nil {
			next.Close()
		}
		return nil, err
	}
	committed = true
	if next != nil && c.apply != nil {
		c.apply(next)
	}
	if c.broadcast != nil {
		c.broadcast(prepared.Config)
	}
	return c.View(ctx)
}

func (c *Config) Activate(ctx context.Context, id string) (*types.ConfigView, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, Errorf(CodeInvalidArgument, "LLM profile id is required")
	}
	stored, err := c.Distribute(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, profile := range stored.GetLlm().GetProviders() {
		if profile.GetId() == id {
			found = true
			break
		}
	}
	if !found {
		return nil, Errorf(CodeNotFound, "LLM profile %q was not found", id)
	}
	next := proto.CloneOf(stored)
	if next.Llm == nil {
		next.Llm = &types.LLMConfig{}
	}
	next.Llm.ActiveProfile = id
	return c.Save(ctx, next)
}

func (c *Config) Distribute(ctx context.Context) (*types.DistributeConfig, error) {
	if c == nil || c.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "config store is not configured")
	}
	_, _, config, err := c.store.GetDistributeConfig(ctx)
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
	view.Recon = &types.ReconView{FofaEmail: config.GetRecon().GetFofaEmail(), FofaKeyConfigured: config.GetRecon().GetFofaKey() != "", HunterTokenConfigured: config.GetRecon().GetHunterToken() != "", HunterApiKeyConfigured: config.GetRecon().GetHunterApiKey() != "", Proxy: config.GetRecon().GetProxy(), Limit: config.GetRecon().GetLimit()}
	view.Scan = &types.ScanConfig{Verify: config.GetScan().GetVerify()}
	view.Search = &types.SearchView{TavilyKeysConfigured: config.GetSearch().GetTavilyKeys() != ""}
	view.Ioa = &types.IOAView{Url: config.GetIoa().GetUrl(), TokenConfigured: config.GetIoa().GetToken() != "", NodeName: config.GetIoa().GetNodeName(), Space: config.GetIoa().GetSpace()}
	view.Agent = &types.AgentConfig{Tools: append([]string(nil), config.GetAgent().GetTools()...), Timeout: config.GetAgent().GetTimeout(), SaveSession: config.GetAgent().GetSaveSession()}
	return view
}
