package web

import (
	"context"

	"connectrpc.com/connect"
	"github.com/chainreactors/aiscan/pkg/rpc/system/systemconnect"
	systempb "github.com/chainreactors/aiscan/pkg/types/system"
)

type connectSystemServer struct {
	systemconnect.UnimplementedSystemServiceHandler
	service   *Service
	pool      *AgentPool
	serverURL string
}

func (s *connectSystemServer) GetStatus(context.Context, *connect.Request[systempb.GetStatusRequest]) (*connect.Response[systempb.GetStatusResponse], error) {
	status := s.service.Status()
	if s.pool != nil {
		status.Agents = uint32(s.pool.Count())
	}
	if s.serverURL != "" {
		status.ServerUrl = s.serverURL
	}
	return connect.NewResponse(&systempb.GetStatusResponse{Status: status}), nil
}

var _ systemconnect.SystemServiceHandler = (*connectSystemServer)(nil)
