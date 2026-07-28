package web

import (
	"context"
	"strings"

	agentprobe "github.com/chainreactors/aiscan/agent/probe"
	"github.com/chainreactors/aiscan/pkg/probe"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// TestConn probes one settings section's external dependencies, resolving blank
// secrets against the stored config, then delegates to pkg/probe. Probe failures
// live inside the response; a returned error only signals an untestable section.
func (s *Service) TestConn(ctx context.Context, section string, in webproto.DistributeConfig) ([]probe.ConnCheck, error) {
	stored, _ := s.storedConfig(ctx)
	return probe.TestConn(ctx, section, toProbeConfig(in), toProbeConfig(stored))
}

func toProbeConfig(dc webproto.DistributeConfig) probe.ProbeConfig {
	return probe.ProbeConfig{
		Cyberhub: probe.CyberhubProbe{URL: dc.Cyberhub.URL, Key: dc.Cyberhub.Key},
		Recon: probe.ReconProbe{
			FofaKey: dc.Recon.FofaKey, HunterToken: dc.Recon.HunterToken,
			HunterAPIKey: dc.Recon.HunterAPIKey, Proxy: dc.Recon.Proxy,
		},
		Search: probe.SearchProbe{TavilyKeys: dc.Search.TavilyKeys},
		IOA:    probe.IOAProbe{URL: dc.IOA.URL, Token: dc.IOA.Token},
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
			for _, profile := range dc.LLM.Providers {
				if profile.ID == profileID {
					return strings.TrimSpace(profile.APIKey)
				}
			}
			return ""
		}
		return strings.TrimSpace(dc.LLM.Active().APIKey)
	}
	return ""
}

// storedConfig returns the config persisted on the server, or ok=false when no
// config store is wired or it cannot be read.
func (s *Service) storedConfig(ctx context.Context) (webproto.DistributeConfig, bool) {
	if s.config == nil {
		return webproto.DistributeConfig{}, false
	}
	dc, err := s.GetDistributeConfig(ctx)
	if err != nil {
		return webproto.DistributeConfig{}, false
	}
	return dc, true
}
