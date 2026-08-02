package web

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type connectAgentServer struct {
	rpc.UnimplementedAgentServiceHandler
	pool  *AgentPool
	local *LocalAgents
}

func (s *connectAgentServer) ListLocalAgents(context.Context, *connect.Request[types.ListLocalAgentsRequest]) (*connect.Response[types.ListLocalAgentsResponse], error) {
	response := &types.ListLocalAgentsResponse{}
	if s.local != nil {
		response.Agents = s.local.List()
	}
	return connect.NewResponse(response), nil
}

func (s *connectAgentServer) LaunchLocalAgent(ctx context.Context, _ *connect.Request[types.LaunchLocalAgentRequest]) (*connect.Response[types.LaunchLocalAgentResponse], error) {
	if s.local == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("local agent launcher is unavailable"))
	}
	agent, err := s.local.Launch(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&types.LaunchLocalAgentResponse{Agent: agent}), nil
}

func (s *connectAgentServer) StopLocalAgent(_ context.Context, req *connect.Request[types.StopLocalAgentRequest]) (*connect.Response[types.StopLocalAgentResponse], error) {
	if s.local == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("local agent launcher is unavailable"))
	}
	if err := s.local.Stop(req.Msg.GetName()); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&types.StopLocalAgentResponse{}), nil
}

func (s *connectAgentServer) ListAgents(context.Context, *connect.Request[types.ListAgentsRequest]) (*connect.Response[types.ListAgentsResponse], error) {
	response := &types.ListAgentsResponse{}
	if s.pool != nil {
		response.Agents = s.pool.List()
	}
	return connect.NewResponse(response), nil
}

var _ rpc.AgentServiceHandler = (*connectAgentServer)(nil)
