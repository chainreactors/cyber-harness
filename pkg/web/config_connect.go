package web

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	agentprobe "github.com/chainreactors/aiscan/agent/probe"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/emptypb"
)

type connectConfigServer struct {
	rpc.UnimplementedConfigServiceHandler
	service *Service
}

func (s *connectConfigServer) TestLLM(ctx context.Context, req *connect.Request[types.LLMProbeRequest]) (*connect.Response[types.LLMProbeResult], error) {
	result, err := s.service.TestLLM(ctx, llmProbeRequest(req.Msg))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&types.LLMProbeResult{Ok: result.OK, Provider: result.Provider, Model: result.Model, LatencyMs: result.LatencyMs, Reply: result.Reply, Error: result.Error}), nil
}

func (s *connectConfigServer) ListModels(ctx context.Context, req *connect.Request[types.LLMProbeRequest]) (*connect.Response[types.ListModelsResult], error) {
	result, err := s.service.ListLLMModels(ctx, llmProbeRequest(req.Msg))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&types.ListModelsResult{Ok: result.OK, Supported: result.Supported, Models: result.Models, Error: result.Error}), nil
}

func (s *connectConfigServer) TestConnection(ctx context.Context, req *connect.Request[types.TestConnectionRequest]) (*connect.Response[types.TestConnectionResponse], error) {
	checks, err := s.service.TestConn(ctx, req.Msg.GetSection(), req.Msg.GetConfig())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	response := &types.TestConnectionResponse{Checks: make([]*types.ConnectionCheck, 0, len(checks))}
	for _, check := range checks {
		response.Checks = append(response.Checks, &types.ConnectionCheck{Name: check.Name, Ok: check.OK, LatencyMs: check.LatencyMs, Detail: check.Detail, Error: check.Error})
	}
	return connect.NewResponse(response), nil
}

func llmProbeRequest(req *types.LLMProbeRequest) agentprobe.LLMProbeRequest {
	if req == nil {
		return agentprobe.LLMProbeRequest{}
	}
	return agentprobe.LLMProbeRequest{ProfileID: req.ProfileId, Provider: req.Provider, BaseURL: req.BaseUrl, APIKey: req.ApiKey, Model: req.Model, Proxy: req.Proxy}
}

func newConnectConfigServer(service *Service) *connectConfigServer {
	return &connectConfigServer{service: service}
}

func (s *connectConfigServer) GetConfig(ctx context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[types.GetConfigResponse], error) {
	view, err := s.service.GetConfigView(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&types.GetConfigResponse{Config: view}), nil
}

func (s *connectConfigServer) UpdateConfig(ctx context.Context, req *connect.Request[types.UpdateConfigRequest]) (*connect.Response[types.UpdateConfigResponse], error) {
	if req.Msg.GetConfig() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config is required"))
	}
	view, err := s.service.SaveConfig(ctx, req.Msg.Config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&types.UpdateConfigResponse{Config: view}), nil
}

func (s *connectConfigServer) ActivateProfile(ctx context.Context, req *connect.Request[types.ActivateProfileRequest]) (*connect.Response[types.ActivateProfileResponse], error) {
	view, err := s.service.ActivateLLMProfile(ctx, req.Msg.ProfileId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&types.ActivateProfileResponse{Config: view}), nil
}

var _ rpc.ConfigServiceHandler = (*connectConfigServer)(nil)
