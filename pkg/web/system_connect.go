package web

import (
	"context"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type connectSystemServer struct {
	rpc.UnimplementedSystemServiceHandler
	service   *Service
	pool      *AgentPool
	serverURL string
}

func (s *connectSystemServer) GetStatus(context.Context, *connect.Request[types.GetStatusRequest]) (*connect.Response[types.GetStatusResponse], error) {
	status := s.service.Status()
	if s.pool != nil {
		status.Agents = uint32(s.pool.Count())
	}
	if s.serverURL != "" {
		status.ServerUrl = s.serverURL
	}
	return connect.NewResponse(&types.GetStatusResponse{Status: status}), nil
}

var _ rpc.SystemServiceHandler = (*connectSystemServer)(nil)
