package web

import (
	"context"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/probe"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// TestConn probes one settings section's external dependencies, resolving blank
// secrets against the stored config, then delegates to pkg/probe. Probe failures
// live inside the response; a returned error only signals an untestable section.
func (s *Service) TestConn(ctx context.Context, section string, in webproto.DistributeConfig) ([]probe.ConnCheck, error) {
	stored, _ := s.storedConfig(ctx)
	return probe.TestConn(ctx, section, in, stored)
}

// TestLLM probes the supplied LLM settings, falling back to the stored API key
// when the request leaves it blank, then delegates to pkg/probe.
func (s *Service) TestLLM(ctx context.Context, req probe.LLMProbeRequest) (probe.LLMTestResult, error) {
	var storedKey string
	if s.config != nil {
		if dc, err := s.GetDistributeConfig(ctx); err == nil {
			storedKey = strings.TrimSpace(dc.LLM.APIKey)
		}
	}
	return probe.TestLLM(ctx, req, storedKey)
}

// ListLLMModels enumerates the models the supplied LLM endpoint advertises,
// falling back to the stored API key when the request leaves it blank, then
// delegates to pkg/probe.
func (s *Service) ListLLMModels(ctx context.Context, req probe.LLMProbeRequest) (probe.LLMModelsResult, error) {
	var storedKey string
	if s.config != nil {
		if dc, err := s.GetDistributeConfig(ctx); err == nil {
			storedKey = strings.TrimSpace(dc.LLM.APIKey)
		}
	}
	return probe.ListLLMModels(ctx, req, storedKey)
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
