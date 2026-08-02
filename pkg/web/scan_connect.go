package web

import (
	"context"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

type connectScanServer struct {
	rpc.UnimplementedScanServiceHandler
	core *scanServiceCore
}

func newConnectScanServer(service *Service) rpc.ScanServiceHandler {
	return &connectScanServer{core: newScanServiceCore(service)}
}

func (s *connectScanServer) SubmitScan(ctx context.Context, req *connect.Request[types.SubmitScanRequest]) (*connect.Response[types.SubmitScanResponse], error) {
	response, err := s.core.SubmitScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) GetScan(ctx context.Context, req *connect.Request[types.GetScanRequest]) (*connect.Response[types.GetScanResponse], error) {
	response, err := s.core.GetScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) ListScans(ctx context.Context, req *connect.Request[types.ListScansRequest]) (*connect.Response[types.ListScansResponse], error) {
	response, err := s.core.ListScans(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) CancelScan(ctx context.Context, req *connect.Request[types.CancelScanRequest]) (*connect.Response[types.CancelScanResponse], error) {
	response, err := s.core.CancelScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) GetScanReport(ctx context.Context, req *connect.Request[types.GetScanReportRequest]) (*connect.Response[types.GetScanReportResponse], error) {
	response, err := s.core.GetScanReport(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

var _ rpc.ScanServiceHandler = (*connectScanServer)(nil)
