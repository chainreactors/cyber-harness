package web

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/chainreactors/aiscan/pkg/rpc/agent/agentconnect"
	agentpb "github.com/chainreactors/aiscan/pkg/types/agent"
)

type connectAgentServer struct {
	agentconnect.UnimplementedAgentServiceHandler
	pool  *AgentPool
	local *LocalAgents
}

func (s *connectAgentServer) ListLocalAgents(context.Context, *connect.Request[agentpb.ListLocalAgentsRequest]) (*connect.Response[agentpb.ListLocalAgentsResponse], error) {
	response := &agentpb.ListLocalAgentsResponse{}
	if s.local != nil {
		response.Agents = s.local.List()
	}
	return connect.NewResponse(response), nil
}

func (s *connectAgentServer) LaunchLocalAgent(ctx context.Context, _ *connect.Request[agentpb.LaunchLocalAgentRequest]) (*connect.Response[agentpb.LaunchLocalAgentResponse], error) {
	if s.local == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("local agent launcher is unavailable"))
	}
	agent, err := s.local.Launch(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentpb.LaunchLocalAgentResponse{Agent: agent}), nil
}

func (s *connectAgentServer) StopLocalAgent(_ context.Context, req *connect.Request[agentpb.StopLocalAgentRequest]) (*connect.Response[agentpb.StopLocalAgentResponse], error) {
	if s.local == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("local agent launcher is unavailable"))
	}
	if err := s.local.Stop(req.Msg.GetName()); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&agentpb.StopLocalAgentResponse{}), nil
}

func (s *connectAgentServer) ListAgents(context.Context, *connect.Request[agentpb.ListAgentsRequest]) (*connect.Response[agentpb.ListAgentsResponse], error) {
	response := &agentpb.ListAgentsResponse{}
	if s.pool != nil {
		response.Agents = s.pool.List()
	}
	return connect.NewResponse(response), nil
}

var _ agentconnect.AgentServiceHandler = (*connectAgentServer)(nil)
