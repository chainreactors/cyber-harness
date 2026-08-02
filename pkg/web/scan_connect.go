package web

import (
	"context"

	"connectrpc.com/connect"
	"github.com/chainreactors/aiscan/pkg/rpc/scan/scanconnect"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
)

type connectScanServer struct {
	scanconnect.UnimplementedScanServiceHandler
	core *scanServiceCore
}

func newConnectScanServer(service *Service) scanconnect.ScanServiceHandler {
	return &connectScanServer{core: newScanServiceCore(service)}
}

func (s *connectScanServer) SubmitScan(ctx context.Context, req *connect.Request[scanpb.SubmitScanRequest]) (*connect.Response[scanpb.SubmitScanResponse], error) {
	response, err := s.core.SubmitScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) GetScan(ctx context.Context, req *connect.Request[scanpb.GetScanRequest]) (*connect.Response[scanpb.GetScanResponse], error) {
	response, err := s.core.GetScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) ListScans(ctx context.Context, req *connect.Request[scanpb.ListScansRequest]) (*connect.Response[scanpb.ListScansResponse], error) {
	response, err := s.core.ListScans(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) CancelScan(ctx context.Context, req *connect.Request[scanpb.CancelScanRequest]) (*connect.Response[scanpb.CancelScanResponse], error) {
	response, err := s.core.CancelScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) GetScanReport(ctx context.Context, req *connect.Request[scanpb.GetScanReportRequest]) (*connect.Response[scanpb.GetScanReportResponse], error) {
	response, err := s.core.GetScanReport(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

var _ scanconnect.ScanServiceHandler = (*connectScanServer)(nil)
