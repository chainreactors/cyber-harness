package web

import (
	"context"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type connectAgentServer struct {
	rpc.UnimplementedAgentServiceHandler
	pool *AgentPool
}

func (s *connectAgentServer) ListAgents(context.Context, *connect.Request[types.ListAgentsRequest]) (*connect.Response[types.ListAgentsResponse], error) {
	response := &types.ListAgentsResponse{}
	if s.pool != nil {
		response.Agents = s.pool.List()
	}
	return connect.NewResponse(response), nil
}

var _ rpc.AgentServiceHandler = (*connectAgentServer)(nil)
