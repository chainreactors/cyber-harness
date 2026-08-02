package web

import (
	"context"
	"strings"

	agentprobe "github.com/chainreactors/aiscan/agent/probe"
	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/probe"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

// TestConn probes one settings section's external dependencies, resolving blank
// secrets against the stored config, then delegates to pkg/probe. Probe failures
// live inside the response; a returned error only signals an untestable section.
func (s *Service) TestConn(ctx context.Context, section string, in *configpb.DistributeConfig) ([]probe.ConnCheck, error) {
	stored, _ := s.storedConfig(ctx)
	return probe.TestConn(ctx, section, toProbeConfig(in), toProbeConfig(stored))
}

func toProbeConfig(dc *configpb.DistributeConfig) probe.ProbeConfig {
	if dc == nil {
		return probe.ProbeConfig{}
	}
	return probe.ProbeConfig{
		Cyberhub: probe.CyberhubProbe{URL: dc.GetCyberhub().GetUrl(), Key: dc.GetCyberhub().GetKey()},
		Recon: probe.ReconProbe{
			FofaKey: dc.GetRecon().GetFofaKey(), HunterToken: dc.GetRecon().GetHunterToken(),
			HunterAPIKey: dc.GetRecon().GetHunterApiKey(), Proxy: dc.GetRecon().GetProxy(),
		},
		Search: probe.SearchProbe{TavilyKeys: dc.GetSearch().GetTavilyKeys()},
		IOA:    probe.IOAProbe{URL: dc.GetIoa().GetUrl(), Token: dc.GetIoa().GetToken()},
	}
}

// TestLLM probes the supplied LLM settings, falling back to the stored API key
// when the request leaves it blank, then delegates to pkg/probe.
func (s *Service) TestLLM(ctx context.Context, req agentprobe.LLMProbeRequest) (agentprobe.LLMTestResult, error) {
	return agentprobe.TestLLM(ctx, req, s.storedLLMAPIKey(ctx, req.ProfileID))
}

// ListLLMModels enumerates the models the supplied LLM endpoint advertises,
// falling back to the stored API key when the request leaves it blank, then
// delegates to pkg/probe.
func (s *Service) ListLLMModels(ctx context.Context, req agentprobe.LLMProbeRequest) (agentprobe.LLMModelsResult, error) {
	return agentprobe.ListLLMModels(ctx, req, s.storedLLMAPIKey(ctx, req.ProfileID))
}

// storedLLMAPIKey returns the requested profile's persisted API key. A blank
// profileID keeps backward compatibility by selecting the active profile.
func (s *Service) storedLLMAPIKey(ctx context.Context, profileID string) string {
	if s.config == nil {
		return ""
	}
	if dc, err := s.GetDistributeConfig(ctx); err == nil {
		profileID = strings.TrimSpace(profileID)
		if profileID != "" {
			for _, profile := range dc.GetLlm().GetProviders() {
				if profile.Id == profileID {
					return strings.TrimSpace(profile.ApiKey)
				}
			}
			return ""
		}
		if active := config.ActiveLLMProvider(dc.GetLlm()); active != nil {
			return strings.TrimSpace(active.ApiKey)
		}
	}
	return ""
}

// storedConfig returns the config persisted on the server, or ok=false when no
// config store is wired or it cannot be read.
func (s *Service) storedConfig(ctx context.Context) (*configpb.DistributeConfig, bool) {
	if s.config == nil {
		return nil, false
	}
	dc, err := s.GetDistributeConfig(ctx)
	if err != nil {
		return nil, false
	}
	return dc, true
}
