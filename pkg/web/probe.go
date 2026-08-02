package web

import (
	"context"

	types "github.com/chainreactors/aiscan/pkg/types"
)

// Compatibility methods keep internal callers on Service while the business
// implementation lives in the protocol-neutral management API.
func (s *Service) TestConn(ctx context.Context, section string, config *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	response, err := s.api.Config.TestConnection(ctx, &types.TestConnectionRequest{Section: section, Config: config})
	if err != nil {
		return nil, err
	}
	return response.Checks, nil
}

func (s *Service) TestLLM(ctx context.Context, request *types.LLMProbeRequest) (*types.LLMProbeResult, error) {
	return s.api.Config.TestLLM(ctx, request)
}

func (s *Service) ListLLMModels(ctx context.Context, request *types.LLMProbeRequest) (*types.ListModelsResult, error) {
	return s.api.Config.ListModels(ctx, request)
}
